// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package components downloads, verifies and installs the third-party
// binaries Covo runs (PHP-FPM, Caddy). Every artifact is pinned in an
// embedded manifest with its SHA-256; nothing is executed or even extracted
// before the hash matches.
package components

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

	_ "embed"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

//go:embed manifest.json
var manifestJSON []byte

// maxFileSize caps a single extracted file (PHP-FPM is ~100 MB).
const maxFileSize = 512 << 20

// markerFile records the verified archive hash inside an install directory.
const markerFile = ".covo-sha256"

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
	Format    string `json:"format,omitempty"`
	URL       string `json:"url"`
	SHA256    string `json:"sha256"`
	// SHA512 is the upstream-published checksum, when upstream publishes one.
	SHA512 string   `json:"sha512,omitempty"`
	Files  []string `json:"files"`
}

// Manifest is the set of pinned artifacts.
type Manifest struct {
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
		return Artifact{}, errcode.Errorf(errcode.NotFound,
			fmt.Sprintf("no %s %s build available for %s (available: %s)", component, spec, arch, strings.Join(m.Versions(component, arch), ", ")))
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

// Dir returns where a is installed: <bin>/<component>/<version>.
func (in Installer) Dir(a Artifact) string {
	return filepath.Join(in.BinDir, a.Component, a.Version)
}

// Install downloads, verifies and extracts a. It is idempotent: an existing
// install whose marker matches a.SHA256 and whose files exist is reused.
func (in Installer) Install(ctx context.Context, a Artifact) (Installed, error) {
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
		if err := extract(archive, stage, a.Files); err != nil {
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
	if err := os.WriteFile(filepath.Join(stage, markerFile), []byte(a.SHA256+"\n"), 0o644); err != nil {
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
	b, err := os.ReadFile(filepath.Join(dir, markerFile))
	if err != nil || strings.TrimSpace(string(b)) != a.SHA256 {
		return false
	}
	for _, f := range a.Files {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			return false
		}
	}
	return true
}

// download fetches a into a temp file and verifies its checksums. The file
// is deleted on any error.
func (in Installer) download(ctx context.Context, a Artifact) (path string, err error) {
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
		return "", errcode.New(errcode.Download, fmt.Errorf("download %s: %w", a.URL, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errcode.Errorf(errcode.Download, fmt.Sprintf("download %s: HTTP %d", a.URL, resp.StatusCode))
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

	h256, h512 := sha256.New(), sha512.New()
	if _, err := io.Copy(io.MultiWriter(f, h256, h512), resp.Body); err != nil {
		return "", errcode.New(errcode.Download, fmt.Errorf("download %s: %w", a.URL, err))
	}
	if err := checkSum("sha256", h256, a.SHA256, a.URL); err != nil {
		return "", err
	}
	if a.SHA512 != "" {
		if err := checkSum("sha512", h512, a.SHA512, a.URL); err != nil {
			return "", err
		}
	}
	return f.Name(), nil
}

func checkSum(name string, h hash.Hash, want, url string) error {
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, want) {
		return errcode.Errorf(errcode.Checksum,
			fmt.Sprintf("%s mismatch for %s: got %s, want %s", name, url, got, want))
	}
	return nil
}

// extract copies the listed regular files out of a .tar.gz into dir. Any
// other entry is ignored; links, traversal and missing files are errors.
func extract(archive, dir string, files []string) error {
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

	want := map[string]bool{}
	for _, name := range files {
		want[name] = false
	}
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
		seen, ok := want[name]
		if !ok {
			continue
		}
		if seen {
			return errcode.Errorf(errcode.Checksum, "archive contains "+name+" twice")
		}
		if hdr.Typeflag != tar.TypeReg {
			return errcode.Errorf(errcode.Checksum, "archive entry "+name+" is not a regular file")
		}
		if hdr.Size > maxFileSize {
			return errcode.Errorf(errcode.Checksum, "archive entry "+name+" is too large")
		}
		if err := writeFile(filepath.Join(dir, filepath.FromSlash(name)), tr, hdr.Size); err != nil {
			return err
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			return errcode.Errorf(errcode.Checksum, "archive does not contain "+name)
		}
	}
	return nil
}

func writeFile(dst string, r io.Reader, size int64) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o755)
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
