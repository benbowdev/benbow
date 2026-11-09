// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package ca creates Covo's local certificate authority: a root and an
// intermediate, both name-constrained to the reserved suffixes Covo serves
// (.localhost, .test, .local) and barred from every IP address. Even if a
// verifier ignores constraints on a trust anchor, the intermediate carries
// them too.
//
// The root private key only exists in memory while the intermediate is
// signed, then it is discarded: nothing can ever mint another intermediate.
// Caddy signs site certificates with the intermediate. Renewing the CA means
// a new root, which must be trusted again.
package ca

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
)

// DefaultName is the name of Covo's CA, as shown in Keychain Access.
// untrust uses it to find the CA when its file is gone.
const DefaultName = "Covo Local CA"

// EnvName overrides the CA name, so that tests never touch the real CA.
const EnvName = "COVO_CA_NAME"

// Name returns the CA name in effect.
func Name() string {
	if n := os.Getenv(EnvName); n != "" {
		return n
	}
	return DefaultName
}

// Suffixes are the only DNS suffixes the CA may sign for.
var Suffixes = []string{"localhost", "test", "local"}

const (
	validity = 10 * 365 * 24 * time.Hour
	// renewBefore regenerates the CA this long before the intermediate expires.
	renewBefore = 30 * 24 * time.Hour
)

// File names inside the CA directory.
const (
	RootCertFile         = "root.crt"
	IntermediateCertFile = "intermediate.crt"
	IntermediateKeyFile  = "intermediate.key"
)

// Dir is the directory holding the CA files.
type Dir string

// RootCert is the path of the root certificate.
func (d Dir) RootCert() string { return filepath.Join(string(d), RootCertFile) }

// IntermediateCert is the path of the intermediate certificate.
func (d Dir) IntermediateCert() string { return filepath.Join(string(d), IntermediateCertFile) }

// IntermediateKey is the path of the intermediate private key.
func (d Dir) IntermediateKey() string { return filepath.Join(string(d), IntermediateKeyFile) }

// Material is a generated CA in PEM form. There is no root key.
type Material struct {
	RootCert         []byte
	IntermediateCert []byte
	IntermediateKey  []byte
}

// Generate creates a new root and intermediate named name.
func Generate(name string, now time.Time) (Material, error) {
	rootKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, fmt.Errorf("generate root key: %w", err)
	}
	interKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Material{}, fmt.Errorf("generate intermediate key: %w", err)
	}
	notBefore := now.Add(-time.Hour).UTC()
	notAfter := now.Add(validity).UTC()

	root := template(name, notBefore, notAfter)
	root.MaxPathLen = 1
	rootDER, err := x509.CreateCertificate(rand.Reader, root, root, &rootKey.PublicKey, rootKey)
	if err != nil {
		return Material{}, fmt.Errorf("create root: %w", err)
	}
	rootCert, err := x509.ParseCertificate(rootDER)
	if err != nil {
		return Material{}, fmt.Errorf("parse root: %w", err)
	}

	inter := template(name+" Intermediate", notBefore, notAfter.Add(-24*time.Hour))
	inter.MaxPathLenZero = true
	interDER, err := x509.CreateCertificate(rand.Reader, inter, rootCert, &interKey.PublicKey, rootKey)
	if err != nil {
		return Material{}, fmt.Errorf("create intermediate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(interKey)
	if err != nil {
		return Material{}, fmt.Errorf("encode intermediate key: %w", err)
	}
	return Material{
		RootCert:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: rootDER}),
		IntermediateCert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: interDER}),
		IntermediateKey:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

func template(cn string, notBefore, notAfter time.Time) *x509.Certificate {
	_, all4, _ := net.ParseCIDR("0.0.0.0/0")
	_, all6, _ := net.ParseCIDR("::/0")
	return &x509.Certificate{
		SerialNumber:                serial(),
		Subject:                     pkix.Name{CommonName: cn, Organization: []string{"Covo"}},
		NotBefore:                   notBefore,
		NotAfter:                    notAfter,
		IsCA:                        true,
		BasicConstraintsValid:       true,
		KeyUsage:                    x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         slices.Clone(Suffixes),
		ExcludedIPRanges:            []*net.IPNet{all4, all6},
	}
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err) // crypto/rand does not fail on macOS
	}
	return n
}

// Constrained reports whether c carries exactly Covo's name constraints.
func Constrained(c *x509.Certificate) bool {
	if !c.PermittedDNSDomainsCritical || len(c.ExcludedIPRanges) != 2 {
		return false
	}
	got := slices.Clone(c.PermittedDNSDomains)
	want := slices.Clone(Suffixes)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return false
	}
	for _, r := range c.ExcludedIPRanges {
		if ones, _ := r.Mask.Size(); ones != 0 {
			return false
		}
	}
	return len(c.PermittedIPRanges) == 0 && len(c.PermittedEmailAddresses) == 0 && len(c.PermittedURIDomains) == 0
}

// Check verifies that m is a usable Covo CA named name at time now: both
// certificates constrained, the intermediate signed by the root and matching
// its key, and not about to expire.
func Check(m Material, name string, now time.Time) error {
	root, err := parseCert(m.RootCert)
	if err != nil {
		return fmt.Errorf("root: %w", err)
	}
	inter, err := parseCert(m.IntermediateCert)
	if err != nil {
		return fmt.Errorf("intermediate: %w", err)
	}
	switch {
	case root.Subject.CommonName != name:
		return fmt.Errorf("root is named %q, want %q", root.Subject.CommonName, name)
	case !root.IsCA || !inter.IsCA:
		return errors.New("not a CA certificate")
	case !Constrained(root) || !Constrained(inter):
		return errors.New("missing Covo's name constraints")
	case now.Add(renewBefore).After(inter.NotAfter):
		return fmt.Errorf("intermediate expires on %s", inter.NotAfter.Format(time.DateOnly))
	}
	if err := root.CheckSignatureFrom(root); err != nil {
		return fmt.Errorf("root is not self-signed: %w", err)
	}
	if err := inter.CheckSignatureFrom(root); err != nil {
		return fmt.Errorf("intermediate is not signed by the root: %w", err)
	}
	block, _ := pem.Decode(m.IntermediateKey)
	if block == nil {
		return errors.New("intermediate key is not PEM")
	}
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("intermediate key: %w", err)
	}
	if !key.PublicKey.Equal(inter.PublicKey) {
		return errors.New("intermediate key does not match its certificate")
	}
	return nil
}

func parseCert(b []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("not a PEM certificate")
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse certificate: %w", err)
	}
	return c, nil
}

// Load reads the CA files from d.
func Load(d Dir) (Material, error) {
	var m Material
	for _, f := range []struct {
		path string
		dst  *[]byte
	}{{d.RootCert(), &m.RootCert}, {d.IntermediateCert(), &m.IntermediateCert}, {d.IntermediateKey(), &m.IntermediateKey}} {
		b, err := os.ReadFile(f.path)
		if errors.Is(err, os.ErrNotExist) {
			return Material{}, errcode.New(errcode.NotFound, fmt.Errorf("read CA: %w", err))
		}
		if err != nil {
			return Material{}, fmt.Errorf("read CA: %w", err)
		}
		*f.dst = b
	}
	return m, nil
}

// Ensure makes sure d holds a valid CA named name, generating a new one if it
// is missing, incomplete, unconstrained or about to expire. It reports
// whether a new CA was created (which then needs to be trusted).
func Ensure(d Dir, name string, now time.Time) (created bool, err error) {
	if m, err := Load(d); err == nil && Check(m, name, now) == nil {
		return false, secure(d)
	}
	m, err := Generate(name, now)
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(string(d), 0o700); err != nil {
		return false, fmt.Errorf("create %s: %w", d, err)
	}
	// Key first: a root on disk always has its intermediate.
	for _, f := range []struct {
		path string
		body []byte
	}{{d.IntermediateKey(), m.IntermediateKey}, {d.IntermediateCert(), m.IntermediateCert}, {d.RootCert(), m.RootCert}} {
		if _, err := fsutil.WriteAtomic(f.path, f.body, 0o600); err != nil {
			return false, err
		}
	}
	return true, secure(d)
}

// secure restricts d to the user: 0700 directory, 0600 files.
func secure(d Dir) error {
	if err := os.Chmod(string(d), 0o700); err != nil {
		return fmt.Errorf("secure %s: %w", d, err)
	}
	for _, f := range []string{d.RootCert(), d.IntermediateCert(), d.IntermediateKey()} {
		if err := os.Chmod(f, 0o600); err != nil {
			return fmt.Errorf("secure %s: %w", f, err)
		}
	}
	return nil
}

// Bundle returns mozilla followed by the root certificate: the trust store
// PHP uses, so that a site can call itself over HTTPS with verification on.
func Bundle(mozilla, rootPEM []byte, name string) []byte {
	var b bytes.Buffer
	b.Write(bytes.TrimRight(mozilla, "\n"))
	fmt.Fprintf(&b, "\n\n%s (added by Covo)\n", name)
	b.Write(rootPEM)
	return b.Bytes()
}

// LeafValidity is how long a certificate from Leaf lasts; Covo makes a new
// one well before (LeafRenewBefore).
const (
	LeafValidity    = 90 * 24 * time.Hour
	LeafRenewBefore = 30 * 24 * time.Hour
)

// Leaf signs a server certificate for names with the intermediate in d, for
// a dev server Covo runs that serves HTTPS itself (Vite next to Laravel).
// The CA and its name constraints are unchanged: names must be under the
// suffixes the CA may sign for. It returns the certificate followed by the
// intermediate, and the new private key, in PEM.
func Leaf(d Dir, names []string, now time.Time) (chain, key []byte, err error) {
	if len(names) == 0 {
		return nil, nil, errcode.Errorf(errcode.InvalidInput, "a certificate needs a name")
	}
	for _, n := range names {
		if !underSuffixes(n) {
			return nil, nil, errcode.Newf(errcode.InvalidInput, "%s is outside the names Covo's CA signs for (.localhost, .test, .local)", n)
		}
	}
	m, err := Load(d)
	if err != nil {
		return nil, nil, err
	}
	inter, err := parseCert(m.IntermediateCert)
	if err != nil {
		return nil, nil, err
	}
	block, _ := pem.Decode(m.IntermediateKey)
	if block == nil {
		return nil, nil, errors.New("read CA: intermediate key is not PEM")
	}
	interKey, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, fmt.Errorf("read CA: intermediate key: %w", err)
	}
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate key: %w", err)
	}
	notAfter := now.Add(LeafValidity)
	if notAfter.After(inter.NotAfter) {
		notAfter = inter.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Covo"}},
		NotBefore:    now.Add(-time.Hour).UTC(),
		NotAfter:     notAfter.UTC(),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     slices.Clone(names),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, inter, &leafKey.PublicKey, interKey)
	if err != nil {
		return nil, nil, fmt.Errorf("sign certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(leafKey)
	if err != nil {
		return nil, nil, fmt.Errorf("encode key: %w", err)
	}
	chain = append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), m.IntermediateCert...)
	return chain, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

// LeafCurrent reports whether chainPEM is a certificate from Leaf for
// exactly names, signed by the CA in d, valid for more than
// LeafRenewBefore after now.
func LeafCurrent(d Dir, chainPEM []byte, names []string, now time.Time) bool {
	leaf, err := parseCert(chainPEM)
	if err != nil || now.Add(LeafRenewBefore).After(leaf.NotAfter) {
		return false
	}
	got, want := slices.Clone(leaf.DNSNames), slices.Clone(names)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		return false
	}
	m, err := Load(d)
	if err != nil {
		return false
	}
	roots, inters := x509.NewCertPool(), x509.NewCertPool()
	if !roots.AppendCertsFromPEM(m.RootCert) || !inters.AppendCertsFromPEM(m.IntermediateCert) {
		return false
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: inters, DNSName: names[0], CurrentTime: now})
	return err == nil
}

func underSuffixes(name string) bool {
	for _, s := range Suffixes {
		if name == s || strings.HasSuffix(name, "."+s) {
			return true
		}
	}
	return false
}
