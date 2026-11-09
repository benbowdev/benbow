// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package repo checks repository-wide conventions.
package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const header = "// SPDX-License-Identifier: GPL-3.0-only\n// Copyright (C) 2026 Andrea Casali\n\n"

// gplSHA256 is the SHA-256 of https://www.gnu.org/licenses/gpl-3.0.txt.
const gplSHA256 = "3972dc9744f6499f0f9b2dbf76696f2ae7ad8af9b23dde66d6af86c9dfb36986"

const root = "../.."

// TestSourcesHaveLicenseHeader covers Go and Swift sources.
func TestSourcesHaveLicenseHeader(t *testing.T) {
	n := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata" || d.Name() == ".build" || path == filepath.Join(root, "app", "build")) {
			return filepath.SkipDir
		}
		ext := filepath.Ext(path)
		if d.IsDir() || (ext != ".go" && ext != ".swift") {
			return nil
		}
		n[ext]++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if d.Name() == "Package.swift" { // must start with swift-tools-version
			_, after, _ := strings.Cut(string(body), "\n")
			body = []byte(after)
		}
		if !strings.HasPrefix(string(body), header) {
			t.Errorf("%s: missing license header", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n[".go"] == 0 || n[".swift"] == 0 {
		t.Fatalf("files found: %v", n)
	}
}

func TestLicenseIsGPLv3(t *testing.T) {
	body, err := os.ReadFile(filepath.Join(root, "LICENSE"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != gplSHA256 {
		t.Fatalf("LICENSE sha256 = %s, want the verbatim GPL-3.0 text from gnu.org", got)
	}
}

func TestRequiredDocs(t *testing.T) {
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "TRADEMARKS.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			t.Error(err)
		}
	}
}

// TestHostsHelperStaysSmall keeps the privileged helper free of Benbow's core
// and third-party code: it runs as root.
func TestHostsHelperStaysSmall(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/benbowdev/benbow/cmd/benbow-hostshelper").Output()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"github.com/benbowdev/benbow/cmd/benbow-hostshelper": true,
		"github.com/benbowdev/benbow/internal/hostshelper":   true,
		"github.com/benbowdev/benbow/internal/hosts":         true,
		"golang.org/x/sys/unix":                              true,
	}
	for _, pkg := range strings.Fields(string(out)) {
		first, _, _ := strings.Cut(pkg, "/")
		if !strings.Contains(first, ".") || strings.HasPrefix(pkg, "vendor/") {
			continue // standard library
		}
		if !allowed[pkg] {
			t.Errorf("benbow-hostshelper depends on %s", pkg)
		}
	}
}
