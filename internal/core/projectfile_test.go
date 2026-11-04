// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/dotenv"
)

func TestRewriteProjectFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "My Shop")
	env := filepath.Join(dir, ".env")
	writeFile(t, env, "APP_NAME=Shop\nDB_HOST=old\n")
	if err := os.Chmod(env, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := editEnvFile(env, []dotenv.Pair{{Key: "DB_HOST", Value: "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, env); !strings.Contains(got, "DB_HOST=127.0.0.1") || !strings.Contains(got, "APP_NAME=Shop") {
		t.Fatalf(".env = %q", got)
	}
	if fi, _ := os.Stat(env); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", fi.Mode().Perm())
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("temporary files left: %v", entries)
	}

	// A wp-config.php that is a symbolic link stays one; its target changes.
	shared := filepath.Join(t.TempDir(), "shared", "wp-config.php")
	writeFile(t, shared, "<?php // old")
	link := filepath.Join(dir, "wp-config.php")
	if err := os.Symlink(shared, link); err != nil {
		t.Fatal(err)
	}
	if err := rewriteProjectFile(link, []byte("<?php // new")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced: %v, %v", fi, err)
	}
	if got := readFile(t, shared); got != "<?php // new" {
		t.Fatalf("target = %q", got)
	}

	if err := rewriteProjectFile(filepath.Join(dir, "missing"), []byte("x")); err == nil {
		t.Error("a missing file was created")
	}
}
