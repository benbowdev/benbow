// Package trust adds Covo's local root CA to, and removes it from, the
// user's login keychain with `security`. No sudo is involved: macOS shows one
// authorization dialog to change the user's trust settings.
package trust

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec // SHA-1 is how the keychain identifies certificates, not a security check.
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// Cert is a parsed root certificate.
type Cert struct {
	CommonName string    `json:"common_name"`
	SHA1       string    `json:"sha1"`
	SHA256     string    `json:"sha256"`
	NotAfter   time.Time `json:"not_after"`
	PEM        []byte    `json:"-"`
}

// ParsePEM parses the first certificate in pemBytes.
func ParsePEM(pemBytes []byte) (Cert, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil || block.Type != "CERTIFICATE" {
		return Cert{}, errcode.Errorf(errcode.InvalidInput, "no PEM certificate found")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return Cert{}, errcode.New(errcode.InvalidInput, fmt.Errorf("parse certificate: %w", err))
	}
	s1 := sha1.Sum(block.Bytes) //nolint:gosec // see import
	s256 := sha256.Sum256(block.Bytes)
	return Cert{
		CommonName: c.Subject.CommonName,
		SHA1:       strings.ToUpper(hex.EncodeToString(s1[:])),
		SHA256:     strings.ToUpper(hex.EncodeToString(s256[:])),
		NotAfter:   c.NotAfter,
		PEM:        pem.EncodeToMemory(block),
	}, nil
}

// ReadCert reads and parses the certificate at path.
func ReadCert(path string) (Cert, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Cert{}, errcode.Errorf(errcode.NotFound, "CA certificate "+path+" does not exist")
	}
	if err != nil {
		return Cert{}, fmt.Errorf("read %s: %w", path, err)
	}
	return ParsePEM(b)
}

// Keychain manages certificates in one keychain file.
type Keychain struct {
	Path string
	// Run executes /usr/bin/security; overridable in tests.
	Run func(ctx context.Context, args ...string) (string, error)
}

// Login returns the current user's login keychain.
func Login() (*Keychain, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	return &Keychain{
		Path: filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		Run:  runSecurity,
	}, nil
}

func runSecurity(ctx context.Context, args ...string) (string, error) {
	// Generous timeout: add-trusted-cert waits for the user to answer a dialog.
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, "/usr/bin/security", args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("security %s: %w: %s", args[0], err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

// Find returns the certificates in the keychain whose Common Name is exactly
// cn. (`security find-certificate -c` matches substrings, so results are
// filtered here.)
func (k *Keychain) Find(ctx context.Context, cn string) ([]Cert, error) {
	out, err := k.Run(ctx, "find-certificate", "-a", "-p", "-c", cn, k.Path)
	if err != nil {
		// Exit status 44: no matching item.
		if strings.Contains(out, "could not be found") {
			return nil, nil
		}
		return nil, errcode.New(errcode.Service, err)
	}
	var certs []Cert
	rest := []byte(out)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		c, err := ParsePEM(pem.EncodeToMemory(block))
		if err != nil {
			continue
		}
		if c.CommonName == cn {
			certs = append(certs, c)
		}
	}
	return certs, nil
}

// Trusted reports whether the user's trust settings mark a certificate with
// cn as a trusted root, and whether it is in this keychain.
func (k *Keychain) Trusted(ctx context.Context, c Cert) (bool, error) {
	inKeychain, err := k.Find(ctx, c.CommonName)
	if err != nil {
		return false, err
	}
	found := false
	for _, kc := range inKeychain {
		if kc.SHA1 == c.SHA1 {
			found = true
		}
	}
	if !found {
		return false, nil
	}
	out, err := k.Run(ctx, "dump-trust-settings")
	if err != nil {
		// "No Trust Settings were found" exits non-zero.
		if strings.Contains(out, "No Trust Settings") {
			return false, nil
		}
		return false, errcode.New(errcode.Service, err)
	}
	return hasTrustEntry(out, c.CommonName), nil
}

// hasTrustEntry looks for "Cert N: <cn>" in dump-trust-settings output.
func hasTrustEntry(out, cn string) bool {
	for line := range strings.Lines(out) {
		if _, name, ok := strings.Cut(strings.TrimSpace(line), ": "); ok &&
			strings.HasPrefix(strings.TrimSpace(line), "Cert ") && name == cn {
			return true
		}
	}
	return false
}

// Add adds c to the keychain as a trusted root for the current user. macOS
// shows an authorization dialog; the call blocks until it is answered.
func (k *Keychain) Add(ctx context.Context, c Cert) error {
	tmp, err := writeTemp(c)
	if err != nil {
		return err
	}
	defer os.Remove(tmp)
	if _, err := k.Run(ctx, "add-trusted-cert", "-r", "trustRoot", "-k", k.Path, tmp); err != nil {
		return errcode.New(errcode.Service, err)
	}
	return nil
}

// Remove removes the trust settings and the keychain items of every
// certificate whose Common Name is exactly cn, plus extra if given (matched by
// SHA-1). It works without the original certificate file. Only what is
// actually present is touched, so removing nothing never shows a dialog.
func (k *Keychain) Remove(ctx context.Context, cn string, extra *Cert) ([]Cert, error) {
	inKeychain, err := k.Find(ctx, cn)
	if err != nil {
		return nil, err
	}
	trustOut, err := k.Run(ctx, "dump-trust-settings")
	if err != nil && !strings.Contains(trustOut, "No Trust Settings") {
		return nil, errcode.New(errcode.Service, err)
	}
	hasTrust := hasTrustEntry(trustOut, cn)

	certs := inKeychain
	present := map[string]bool{}
	for _, c := range inKeychain {
		present[c.SHA1] = true
	}
	if extra != nil && !present[extra.SHA1] && hasTrust {
		certs = append(certs, *extra)
	}
	var removed []Cert
	for _, c := range certs {
		if hasTrust {
			tmp, err := writeTemp(c)
			if err != nil {
				return removed, err
			}
			// Trust settings are keyed by certificate, not keychain item.
			out, err := k.Run(ctx, "remove-trusted-cert", tmp)
			_ = os.Remove(tmp)
			if err != nil && !strings.Contains(out, "could not be found") {
				return removed, errcode.New(errcode.Service, err)
			}
		}
		if present[c.SHA1] {
			if _, err := k.Run(ctx, "delete-certificate", "-Z", c.SHA1, k.Path); err != nil {
				return removed, errcode.New(errcode.Service, err)
			}
		}
		removed = append(removed, c)
	}
	return removed, nil
}

func writeTemp(c Cert) (string, error) {
	f, err := os.CreateTemp("", "covo-ca-*.pem")
	if err != nil {
		return "", fmt.Errorf("write temp certificate: %w", err)
	}
	if _, err := f.Write(c.PEM); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write temp certificate: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("write temp certificate: %w", err)
	}
	return f.Name(), nil
}
