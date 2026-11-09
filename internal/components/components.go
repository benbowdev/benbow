// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package components downloads, verifies and installs the third-party
// binaries Benbow runs (PHP-FPM, Caddy). Every artifact is pinned in an
// embedded manifest with its SHA-256; nothing is executed or even extracted
// before the hash matches.
package components

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/md5" //nolint:gosec // only to match the checksum MySQL publishes; SHA-256 is the real check
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	_ "embed"

	"golang.org/x/sys/unix"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
)

//go:embed manifest.json
var manifestJSON []byte

// maxFileSize caps a single extracted file (PHP-FPM is ~100 MB).
const maxFileSize = 512 << 20

// MarkerFile records the verified archive hash inside an install directory.
const MarkerFile = ".benbow-sha256"

// Artifact formats.
const (
	FormatTarGz = ""     // a .tar.gz from which Files are extracted
	FormatFile  = "file" // a single file, saved as Files[0]
)

// ArchAll marks an artifact that does not depend on the architecture.
const ArchAll = "all"

// Artifact is one downloadable, pinned archive or file.
type Artifact struct {
	Component string `json:"component"`
	Version   string `json:"version"`
	Arch      string `json:"arch"` // GOARCH or ArchAll
	// For is the exact version of the component this one belongs to (the
	// PHP version an xdebug.so was built for), or empty.
	For    string `json:"for,omitempty"`
	Format string `json:"format,omitempty"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	// SHA512 is the upstream-published checksum, when upstream publishes one.
	SHA512 string `json:"sha512,omitempty"`
	// MD5 is the upstream-published checksum when upstream publishes nothing
	// better (MySQL); it is checked in addition to SHA256.
	MD5 string `json:"md5,omitempty"`
	// MinMacOS is the oldest macOS the artifact runs on ("15.0"), if it
	// needs more than Benbow itself.
	MinMacOS string `json:"min_macos,omitempty"`
	// Strip is a leading directory removed from every archive entry name.
	Strip string `json:"strip,omitempty"`
	// Files are extracted from a .tar.gz. An entry ending in "/" extracts the
	// whole directory below it. Symbolic links are only accepted when listed
	// one by one, relative, and pointing at another extracted file.
	Files []string `json:"files"`
}

// Manifest is the set of pinned artifacts.
type Manifest struct {
	// Serial grows with every published manifest (YYYYMMDDnn). A signed
	// manifest is only accepted if it is newer than what Benbow has.
	Serial int64 `json:"serial"`
	// Expires is set on published manifests only (benbow-binaries signs them
	// again before it passes): see CheckFresh.
	Expires   *time.Time `json:"expires,omitempty"`
	Artifacts []Artifact `json:"artifacts"`
}

// Embedded returns the manifest compiled into the binary.
func Embedded() (Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		return Manifest{}, fmt.Errorf("parse embedded manifest: %w", err)
	}
	return m, nil
}

// Resolve finds the artifact of component matching spec for the running
// architecture. spec is either an exact version ("8.4.23") or a prefix
// ("8.4"), in which case the newest matching version wins.
func (m Manifest) Resolve(component, spec string) (Artifact, error) {
	return m.resolve(component, spec, runtime.GOARCH)
}

// ResolveFor finds the newest artifact of component built for exactly
// version forVersion of another component, for the running architecture.
func (m Manifest) ResolveFor(component, forVersion string) (Artifact, error) {
	return m.resolveFor(component, forVersion, runtime.GOARCH)
}

func (m Manifest) resolveFor(component, forVersion, arch string) (Artifact, error) {
	var best *Artifact
	for i, a := range m.Artifacts {
		if a.Component != component || a.For != forVersion || (a.Arch != arch && a.Arch != ArchAll) {
			continue
		}
		if best == nil || store.CompareVersions(a.Version, best.Version) > 0 {
			best = &m.Artifacts[i]
		}
	}
	if best == nil {
		return Artifact{}, errcode.Newf(errcode.NotFound,
			"no %s build for %s on %s", component, forVersion, arch)

	}
	return *best, nil
}

func (m Manifest) resolve(component, spec, arch string) (Artifact, error) {
	var best *Artifact
	for i, a := range m.Artifacts {
		if a.Component != component || (a.Arch != arch && a.Arch != ArchAll) {
			continue
		}
		if spec != "" && a.Version != spec && !strings.HasPrefix(a.Version, spec+".") {
			continue
		}
		if best == nil || store.CompareVersions(a.Version, best.Version) > 0 {
			best = &m.Artifacts[i]
		}
	}
	if best == nil {
		return Artifact{}, errcode.Newf(errcode.NotFound,
			"no %s %s build available for %s (available: %s)", component, spec, arch, strings.Join(m.Versions(component, arch), ", "))

	}
	return *best, nil
}

// Versions lists the versions of component available for arch.
func (m Manifest) Versions(component, arch string) []string {
	var out []string
	for _, a := range m.Artifacts {
		if a.Component == component && (a.Arch == arch || a.Arch == ArchAll) && !slices.Contains(out, a.Version) {
			out = append(out, a.Version)
		}
	}
	slices.SortFunc(out, store.CompareVersions)
	return out
}

// Installer installs artifacts under binDir, using tmpDir for downloads.
type Installer struct {
	BinDir string
	TmpDir string
	Client *http.Client
}

// Installed describes an artifact on disk.
type Installed struct {
	Artifact
	Dir string `json:"dir"`
	// Fresh is true when this call downloaded it, false if already present.
	Fresh bool `json:"fresh"`
}

// Dir returns where a is installed: <bin>/<component>/<version>, plus
// /<for> for an artifact built for one version of another component.
func (in Installer) Dir(a Artifact) string {
	if a.For != "" {
		return filepath.Join(in.BinDir, a.Component, a.Version, a.For)
	}
	return filepath.Join(in.BinDir, a.Component, a.Version)
}

// Install downloads, verifies and extracts a. It is idempotent: an existing
// install whose marker matches a.SHA256 and whose files exist is reused.
func (in Installer) Install(ctx context.Context, a Artifact) (Installed, error) {
	if err := checkMacOS(a); err != nil {
		return Installed{}, err
	}
	dest := in.Dir(a)
	if in.verified(dest, a) {
		return Installed{Artifact: a, Dir: dest}, nil
	}
	if err := os.MkdirAll(in.TmpDir, 0o700); err != nil {
		return Installed{}, fmt.Errorf("create tmp dir: %w", err)
	}

	archive, err := in.download(ctx, a)
	if err != nil {
		return Installed{}, err
	}
	defer os.Remove(archive)

	stage, err := os.MkdirTemp(in.TmpDir, "stage-")
	if err != nil {
		return Installed{}, fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stage) // no-op after a successful rename

	switch a.Format {
	case FormatTarGz:
		if err := extract(archive, stage, a.Strip, a.Files); err != nil {
			return Installed{}, err
		}
	case FormatFile:
		if len(a.Files) != 1 || path.Base(a.Files[0]) != a.Files[0] {
			return Installed{}, errcode.Errorf(errcode.Internal, "file artifact "+a.Component+" must list exactly one plain file name")
		}
		if err := os.Rename(archive, filepath.Join(stage, a.Files[0])); err != nil {
			return Installed{}, fmt.Errorf("move %s: %w", a.Files[0], err)
		}
		if err := os.Chmod(filepath.Join(stage, a.Files[0]), 0o644); err != nil {
			return Installed{}, fmt.Errorf("chmod %s: %w", a.Files[0], err)
		}
	default:
		return Installed{}, errcode.Errorf(errcode.Internal, "unknown artifact format "+a.Format)
	}
	if err := os.WriteFile(filepath.Join(stage, MarkerFile), []byte(a.SHA256+"\n"), 0o644); err != nil {
		return Installed{}, fmt.Errorf("write marker: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return Installed{}, fmt.Errorf("create %s: %w", filepath.Dir(dest), err)
	}
	// A partial or tampered previous install is replaced as a whole.
	if err := os.RemoveAll(dest); err != nil {
		return Installed{}, fmt.Errorf("remove stale %s: %w", dest, err)
	}
	if err := os.Chmod(stage, 0o755); err != nil {
		return Installed{}, fmt.Errorf("chmod staging dir: %w", err)
	}
	if err := os.Rename(stage, dest); err != nil {
		return Installed{}, fmt.Errorf("move into place: %w", err)
	}
	return Installed{Artifact: a, Dir: dest, Fresh: true}, nil
}

func (in Installer) verified(dir string, a Artifact) bool {
	b, err := os.ReadFile(filepath.Join(dir, MarkerFile))
	if err != nil || strings.TrimSpace(string(b)) != a.SHA256 {
		return false
	}
	for _, f := range a.Files {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(f))); err != nil {
			return false
		}
	}
	return true
}

// downloadAttempts and retryDelay bound retries of transient failures
// (network errors, HTTP 5xx). A checksum mismatch is never retried.
const downloadAttempts = 3

var retryDelay = 2 * time.Second

// download fetches a, retrying transient failures.
func (in Installer) download(ctx context.Context, a Artifact) (string, error) {
	for attempt := 1; ; attempt++ {
		path, err := in.downloadOnce(ctx, a)
		if err == nil {
			return path, nil
		}
		if attempt == downloadAttempts || !errors.Is(err, errTransient) {
			return "", err
		}
		select {
		case <-ctx.Done():
			return "", errcode.New(errcode.Download, fmt.Errorf("download %s: %w", a.URL, ctx.Err()))
		case <-time.After(time.Duration(attempt) * retryDelay):
		}
	}
}

// errTransient marks failures worth retrying.
var errTransient = errors.New("transient")

type transientError struct{ err error }

func (e transientError) Error() string   { return e.err.Error() }
func (e transientError) Unwrap() []error { return []error{e.err, errTransient} }

// downloadOnce fetches a into a temp file and verifies its checksums. The
// file is deleted on any error.
func (in Installer) downloadOnce(ctx context.Context, a Artifact) (path string, err error) {
	client := in.Client
	if client == nil {
		client = http.DefaultClient
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", errcode.New(errcode.Download, fmt.Errorf("download %s: %w", a.URL, err))
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", errcode.New(errcode.Download, transientError{fmt.Errorf("download %s: %w", a.URL, err)})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("download %s: HTTP %d", a.URL, resp.StatusCode)
		if resp.StatusCode >= 500 {
			err = transientError{err}
		}
		return "", errcode.New(errcode.Download, err)
	}

	f, err := os.CreateTemp(in.TmpDir, "download-")
	if err != nil {
		return "", fmt.Errorf("create download file: %w", err)
	}
	defer func() {
		_ = f.Close()
		if err != nil {
			_ = os.Remove(f.Name())
		}
	}()

	h256, h512, hmd5 := sha256.New(), sha512.New(), md5.New() //nolint:gosec // see import
	if _, err := io.Copy(io.MultiWriter(f, h256, h512, hmd5), resp.Body); err != nil {
		return "", errcode.New(errcode.Download, transientError{fmt.Errorf("download %s: %w", a.URL, err)})
	}
	if err := checkSum("sha256", h256, a.SHA256, a.URL); err != nil {
		return "", err
	}
	if a.SHA512 != "" {
		if err := checkSum("sha512", h512, a.SHA512, a.URL); err != nil {
			return "", err
		}
	}
	if a.MD5 != "" {
		if err := checkSum("md5", hmd5, a.MD5, a.URL); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

func checkSum(name string, h hash.Hash, want, url string) error {
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return errcode.Newf(errcode.Checksum,
			"%s mismatch for %s: got %s, want %s", name, url, got, want)

	}
	return nil
}

// extract copies the listed entries out of a .tar.gz into dir, after removing
// the strip directory from their names. Any other entry is ignored.
// Traversal, unlisted links, links leaving dir or pointing at nothing
// extracted, and missing entries are errors.
func extract(archive, dir, strip string, files []string) error {
	f, err := os.Open(archive)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("read archive: %w", err)
	}
	defer gz.Close()

	want := map[string]bool{} // exact entries, and whether they were seen
	var trees []string        // directory entries ("share/english/")
	for _, name := range files {
		if strings.HasSuffix(name, "/") {
			trees = append(trees, name)
		}
		want[name] = false
	}
	inTree := func(name string) (string, bool) {
		for _, t := range trees {
			if strings.HasPrefix(name, t) {
				return t, true
			}
		}
		return "", false
	}
	extracted := map[string]bool{}
	links := map[string]string{} // link name -> target, relative to dir

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "./"))
		if strip != "" {
			rest, ok := strings.CutPrefix(name, strip+"/")
			if !ok {
				continue
			}
			name = rest
		}
		if name == "." || name == ".." || strings.HasPrefix(name, "../") || path.IsAbs(name) {
			continue
		}
		seen, exact := want[name]
		tree, below := inTree(name)
		if !exact && !below {
			continue
		}
		if exact && seen || extracted[name] {
			return errcode.Errorf(errcode.Checksum, "archive contains "+name+" twice")
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		switch hdr.Typeflag {
		case tar.TypeReg:
			if hdr.Size > maxFileSize {
				return errcode.Errorf(errcode.Checksum, "archive entry "+name+" is too large")
			}
			if err := writeFile(dst, tr, hdr.Size, os.FileMode(hdr.Mode).Perm()&0o755|0o644); err != nil {
				return err
			}
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return fmt.Errorf("create %s: %w", dst, err)
			}
		case tar.TypeSymlink:
			if !exact {
				return errcode.Errorf(errcode.Checksum, "archive entry "+name+" is a link that is not listed")
			}
			target := path.Clean(path.Join(path.Dir(name), hdr.Linkname))
			if path.IsAbs(hdr.Linkname) || target == ".." || strings.HasPrefix(target, "../") {
				return errcode.Errorf(errcode.Checksum, "archive link "+name+" points outside the install directory")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
			}
			if err := os.Symlink(hdr.Linkname, dst); err != nil {
				return fmt.Errorf("create link %s: %w", dst, err)
			}
			links[name] = target
		default:
			return errcode.Errorf(errcode.Checksum, "archive entry "+name+" is not a regular file")
		}
		extracted[name] = true
		if exact {
			want[name] = true
		} else {
			want[tree] = true
		}
	}
	for name, seen := range want {
		if !seen {
			return errcode.Errorf(errcode.Checksum, "archive does not contain "+name)
		}
	}
	for name, target := range links {
		if fi, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(target))); err != nil || !fi.Mode().IsRegular() || !extracted[target] {
			return errcode.Errorf(errcode.Checksum, "archive link "+name+" does not point at an extracted file")
		}
	}
	return nil
}

func writeFile(dst string, r io.Reader, size int64, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dst, err)
	}
	if _, err := io.CopyN(out, r, size); err != nil {
		_ = out.Close()
		return fmt.Errorf("extract %s: %w", dst, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("extract %s: %w", dst, err)
	}
	return nil
}

// macOSVersion is the running macOS version ("26.2"); a variable for tests.
var macOSVersion = func() string {
	v, _ := unix.Sysctl("kern.osproductversion")
	return v
}

func checkMacOS(a Artifact) error {
	if a.MinMacOS == "" {
		return nil
	}
	if v := macOSVersion(); v != "" && store.CompareVersions(v, a.MinMacOS) < 0 {
		return errcode.Newf(errcode.Unsupported, "%s %s needs macOS %s or later (this Mac has %s)", a.Component, a.Version, a.MinMacOS, v)
	}
	return nil
}
