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

func TestGoFilesHaveLicenseHeader(t *testing.T) {
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == "testdata") {
			return filepath.SkipDir
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") {
			return nil
		}
		n++
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(string(body), header) {
			t.Errorf("%s: missing license header", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no Go files found")
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

// TestHostsHelperStaysSmall keeps the privileged helper free of Covo's core
// and third-party code: it runs as root.
func TestHostsHelperStaysSmall(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "github.com/getcovo/covo/cmd/covo-hostshelper").Output()
	if err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"github.com/getcovo/covo/cmd/covo-hostshelper": true,
		"github.com/getcovo/covo/internal/hostshelper": true,
		"github.com/getcovo/covo/internal/hosts":       true,
		"golang.org/x/sys/unix":                        true,
	}
	for _, pkg := range strings.Fields(string(out)) {
		first, _, _ := strings.Cut(pkg, "/")
		if !strings.Contains(first, ".") || strings.HasPrefix(pkg, "vendor/") {
			continue // standard library
		}
		if !allowed[pkg] {
			t.Errorf("covo-hostshelper depends on %s", pkg)
		}
	}
}
