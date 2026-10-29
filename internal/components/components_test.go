// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package components

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/md5"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

type entry struct {
	name string
	body string
	typ  byte
	link string // symlink target; /etc/passwd if empty
}

func tarGz(t *testing.T, entries ...entry) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		hdr := &tar.Header{Name: e.name, Mode: 0o755, Size: int64(len(e.body)), Typeflag: typ}
		if typ != tar.TypeReg {
			hdr.Size = 0
			hdr.Linkname = e.link
			if hdr.Linkname == "" {
				hdr.Linkname = "/etc/passwd"
			}
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func sum256(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func sum512(b []byte) string { s := sha512.Sum512(b); return hex.EncodeToString(s[:]) }
func summd5(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) } //nolint:gosec // test of the MD5 check

func serve(t *testing.T, body []byte) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

func newInstaller(t *testing.T) Installer {
	root := filepath.Join(t.TempDir(), "Covo Home")
	return Installer{BinDir: filepath.Join(root, "bin"), TmpDir: filepath.Join(root, "tmp")}
}

func TestInstall(t *testing.T) {
	good := tarGz(t, entry{name: "php-fpm", body: "#!/bin/sh\necho fpm\n"}, entry{name: "README", body: "ignored"})
	tests := []struct {
		name     string
		archive  []byte
		mutate   func(a *Artifact)
		wantCode string
	}{
		{"ok", good, nil, ""},
		{"ok with sha512", good, func(a *Artifact) { a.SHA512 = sum512(good) }, ""},
		{"sha256 mismatch", good, func(a *Artifact) { a.SHA256 = sum256([]byte("other")) }, errcode.Checksum},
		{"sha512 mismatch", good, func(a *Artifact) { a.SHA512 = sum512([]byte("other")) }, errcode.Checksum},
		{"ok with md5", good, func(a *Artifact) { a.MD5 = summd5(good) }, ""},
		{"md5 mismatch", good, func(a *Artifact) { a.MD5 = summd5([]byte("other")) }, errcode.Checksum},
		{"missing file", tarGz(t, entry{name: "other", body: "x"}), nil, errcode.Checksum},
		{"symlink entry", tarGz(t, entry{name: "php-fpm", typ: tar.TypeSymlink}), nil, errcode.Checksum},
		{"traversal ignored and file missing", tarGz(t, entry{name: "../php-fpm", body: "x"}), nil, errcode.Checksum},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := serve(t, tt.archive)
			in := newInstaller(t)
			a := Artifact{Component: "php", Version: "8.4.23", Arch: "arm64", URL: srv.URL, SHA256: sum256(tt.archive), Files: []string{"php-fpm"}}
			if tt.mutate != nil {
				tt.mutate(&a)
			}
			got, err := in.Install(context.Background(), a)
			if tt.wantCode != "" {
				if errcode.Code(err) != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				if _, err := os.Stat(in.Dir(a)); !os.IsNotExist(err) {
					t.Fatalf("install dir exists after failure: %v", err)
				}
				assertEmptyTmp(t, in.TmpDir)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !got.Fresh {
				t.Error("first install should be fresh")
			}
			fi, err := os.Stat(filepath.Join(got.Dir, "php-fpm"))
			if err != nil || fi.Mode().Perm()&0o100 == 0 {
				t.Fatalf("php-fpm not installed executable: %v", err)
			}
			if _, err := os.Stat(filepath.Join(got.Dir, "README")); !os.IsNotExist(err) {
				t.Error("unlisted files must not be extracted")
			}
			assertEmptyTmp(t, in.TmpDir)
		})
	}
}

// TestInstallTree covers the MySQL layout: a top directory to strip, whole
// directories and a listed relative link.
func TestInstallTree(t *testing.T) {
	const top = "mysql-8.4.13-macos15-arm64/"
	files := []string{"bin/mysqld", "bin/libp.dylib", "lib/libp.dylib", "share/english/"}
	base := []entry{
		{name: top, typ: tar.TypeDir},
		{name: top + "bin/mysqld", body: "#!/bin/sh\n"},
		{name: top + "bin/mysqld-debug", body: "big"},
		{name: top + "bin/libp.dylib", typ: tar.TypeSymlink, link: "../lib/libp.dylib"},
		{name: top + "lib/libp.dylib", body: "lib"},
		{name: top + "share/english", typ: tar.TypeDir},
		{name: top + "share/english/errmsg.sys", body: "msgs"},
		{name: top + "share/english/sub/x", body: "x"},
		{name: top + "share/french/errmsg.sys", body: "fr"},
		{name: "other-top/bin/mysqld", body: "wrong"},
	}
	with := func(extra ...entry) []entry { return append(append([]entry{}, base...), extra...) }
	tests := []struct {
		name    string
		entries []entry
		files   []string
		ok      bool
	}{
		{"ok", base, files, true},
		{"link escapes", with(entry{name: top + "bin/evil", typ: tar.TypeSymlink, link: "../../../etc/passwd"}), append(files, "bin/evil"), false},
		{"absolute link", with(entry{name: top + "bin/abs", typ: tar.TypeSymlink, link: "/etc/passwd"}), append(files, "bin/abs"), false},
		{"link in tree", with(entry{name: top + "share/english/l", typ: tar.TypeSymlink, link: "errmsg.sys"}), files, false},
		{"link to unextracted file", base, []string{"bin/mysqld", "bin/libp.dylib", "share/english/"}, false},
		{"missing tree", base, append(files, "share/german/"), false},
		{"hard link in tree", with(entry{name: top + "share/english/h", typ: tar.TypeLink}), files, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			archive := tarGz(t, tt.entries...)
			srv, _ := serve(t, archive)
			in := newInstaller(t)
			a := Artifact{Component: "mysql", Version: "8.4.13", Arch: "arm64", URL: srv.URL, SHA256: sum256(archive), Strip: strings.TrimSuffix(top, "/"), Files: tt.files}
			got, err := in.Install(context.Background(), a)
			if !tt.ok {
				if errcode.Code(err) != errcode.Checksum {
					t.Fatalf("err = %v, want %s", err, errcode.Checksum)
				}
				if _, err := os.Stat(in.Dir(a)); !os.IsNotExist(err) {
					t.Fatalf("install dir exists after failure: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			for name, want := range map[string]string{
				"bin/mysqld": "#!/bin/sh\n", "bin/libp.dylib": "lib", "share/english/errmsg.sys": "msgs", "share/english/sub/x": "x",
			} {
				b, err := os.ReadFile(filepath.Join(got.Dir, name))
				if err != nil || string(b) != want {
					t.Errorf("%s = %q, %v", name, b, err)
				}
			}
			for _, name := range []string{"bin/mysqld-debug", "share/french", "mysql-8.4.13-macos15-arm64"} {
				if _, err := os.Lstat(filepath.Join(got.Dir, name)); !os.IsNotExist(err) {
					t.Errorf("%s must not be extracted", name)
				}
			}
			if !in.verified(got.Dir, a) {
				t.Error("install not verified")
			}
		})
	}
}

func TestInstallIsIdempotent(t *testing.T) {
	archive := tarGz(t, entry{name: "php-fpm", body: "x"})
	srv, hits := serve(t, archive)
	in := newInstaller(t)
	a := Artifact{Component: "php", Version: "8.4.23", URL: srv.URL, SHA256: sum256(archive), Files: []string{"php-fpm"}}
	for i := range 2 {
		got, err := in.Install(context.Background(), a)
		if err != nil {
			t.Fatal(err)
		}
		if got.Fresh != (i == 0) {
			t.Fatalf("install %d: Fresh = %v", i, got.Fresh)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("downloaded %d times, want 1", hits.Load())
	}

	// A damaged install (binary deleted) is repaired.
	if err := os.Remove(filepath.Join(in.Dir(a), "php-fpm")); err != nil {
		t.Fatal(err)
	}
	if got, err := in.Install(context.Background(), a); err != nil || !got.Fresh {
		t.Fatalf("repair: fresh=%v err=%v", got.Fresh, err)
	}
}

func TestInstallFile(t *testing.T) {
	body := []byte("-----BEGIN CERTIFICATE-----\n")
	srv, _ := serve(t, body)
	in := newInstaller(t)
	a := Artifact{Component: "cabundle", Version: "2026-10-20", Arch: ArchAll, Format: FormatFile,
		URL: srv.URL, SHA256: sum256(body), Files: []string{"cacert.pem"}}
	got, err := in.Install(context.Background(), a)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(got.Dir, "cacert.pem"))
	if err != nil || string(b) != string(body) {
		t.Fatalf("installed file = %q, %v", b, err)
	}
	assertEmptyTmp(t, in.TmpDir)

	a.SHA256 = sum256([]byte("other"))
	a.Version = "2026-10-21"
	if _, err := in.Install(context.Background(), a); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("err = %v, want checksum_mismatch", err)
	}
	a.SHA256 = sum256(body)
	a.Files = []string{"../escape.pem"}
	if _, err := in.Install(context.Background(), a); err == nil {
		t.Fatal("a file artifact with a path must be refused")
	}
}

func TestInstallHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	in := newInstaller(t)
	_, err := in.Install(context.Background(), Artifact{Component: "php", Version: "1", URL: srv.URL, SHA256: "x", Files: []string{"php-fpm"}})
	if errcode.Code(err) != errcode.Download {
		t.Fatalf("err = %v, want download_failed", err)
	}
}

func assertEmptyTmp(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("tmp dir not cleaned: %v", entries)
	}
}

func TestResolve(t *testing.T) {
	m := Manifest{Artifacts: []Artifact{
		{Component: "php", Version: "8.4.2", Arch: "arm64"},
		{Component: "php", Version: "8.4.23", Arch: "arm64"},
		{Component: "php", Version: "8.4.30", Arch: "amd64"},
		{Component: "php", Version: "8.2.32", Arch: "arm64"},
		{Component: "caddy", Version: "2.11.7", Arch: "arm64"},
		{Component: "cabundle", Version: "2026-10-20", Arch: ArchAll},
	}}
	tests := []struct {
		component, spec, want string
		wantErr               bool
	}{
		{"php", "8.4", "8.4.23", false},
		{"php", "8.4.2", "8.4.2", false},
		{"php", "8.2", "8.2.32", false},
		{"php", "8", "8.4.23", false},
		{"php", "8.3", "", true},
		{"php", "8.4.3", "", true},
		{"caddy", "", "2.11.7", false},
		{"cabundle", "", "2026-10-20", false},
	}
	for _, tt := range tests {
		t.Run(tt.component+"@"+tt.spec, func(t *testing.T) {
			got, err := m.resolve(tt.component, tt.spec, "arm64")
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if got.Version != tt.want {
				t.Fatalf("version = %q, want %q", got.Version, tt.want)
			}
		})
	}
}

func TestEmbeddedManifest(t *testing.T) {
	m, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range m.Artifacts {
		if len(a.SHA256) != 64 || a.URL == "" || len(a.Files) == 0 {
			t.Errorf("incomplete artifact %+v", a)
		}
	}
	if a, err := m.resolve("mysql", "8.4", "arm64"); err != nil || a.MD5 == "" || a.Strip == "" {
		t.Errorf("mysql arm64: %+v, %v", a, err)
	}
	// Oracle ships no Intel build of the pinned 8.4 release: arm64 only.
	if _, err := m.resolve("mysql", "8.4", "amd64"); errcode.Code(err) != errcode.NotFound {
		t.Errorf("mysql amd64: %v", err)
	}
	if _, err := m.resolve("cabundle", "", "arm64"); err != nil {
		t.Errorf("cabundle: %v", err)
	}
	for _, spec := range []string{"8.4", "8.2"} {
		for _, arch := range []string{"arm64", "amd64"} {
			if _, err := m.resolve("php", spec, arch); err != nil {
				t.Errorf("php %s/%s: %v", spec, arch, err)
			}
		}
	}
}
