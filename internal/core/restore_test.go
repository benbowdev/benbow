// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/benbowdev/benbow/internal/paths"
)

func restoreCore(t *testing.T) *Core {
	t.Helper()
	c := groupsCore(t)
	c.Layout = paths.Layout{Lock: c.Layout.Lock, Data: filepath.Join(t.TempDir(), "data")}
	return c
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A restore killed after moving the folder aside leaves a half-cloned
// folder: recovery puts the original back.
func TestRecoverRestoreInterruptedMidClone(t *testing.T) {
	ctx := context.Background()
	c := restoreCore(t)
	root := filepath.Join(t.TempDir(), "my shop")
	aside := root + ".benbow-restore-101010"
	writeFile(t, filepath.Join(aside, "index.php"), "original")
	writeFile(t, filepath.Join(aside, ".git", "HEAD"), "ref")
	writeFile(t, filepath.Join(root, "index.php"), "half")
	if err := c.writeRestoreJournal(restoreJournal{Site: "shop", Root: root, Aside: aside}); err != nil {
		t.Fatal(err)
	}

	sites, err := c.RecoverRestores(ctx)
	if err != nil || len(sites) != 1 || sites[0] != "shop" {
		t.Fatalf("RecoverRestores = %v, %v", sites, err)
	}
	if got := readFile(t, filepath.Join(root, "index.php")); got != "original" {
		t.Fatalf("index.php = %q", got)
	}
	if got := readFile(t, filepath.Join(root, ".git", "HEAD")); got != "ref" {
		t.Fatalf(".git/HEAD = %q", got)
	}
	if _, err := os.Lstat(aside); !os.IsNotExist(err) {
		t.Fatalf("aside still there: %v", err)
	}
	if _, err := os.Lstat(c.restoreJournalPath("shop")); !os.IsNotExist(err) {
		t.Fatalf("journal still there: %v", err)
	}
	// Idempotent: nothing left to do.
	if sites, err := c.RecoverRestores(ctx); err != nil || len(sites) != 0 {
		t.Fatalf("second RecoverRestores = %v, %v", sites, err)
	}
}

// Killed before the folder moved, or after the restore had finished: the
// folder is whole, only the journal goes.
func TestRecoverRestoreNothingMoved(t *testing.T) {
	c := restoreCore(t)
	root := filepath.Join(t.TempDir(), "shop")
	writeFile(t, filepath.Join(root, "index.php"), "current")
	if err := c.writeRestoreJournal(restoreJournal{Site: "shop", Root: root, Aside: root + ".benbow-restore-1"}); err != nil {
		t.Fatal(err)
	}
	sites, err := c.RecoverRestores(context.Background())
	if err != nil || len(sites) != 0 {
		t.Fatalf("RecoverRestores = %v, %v", sites, err)
	}
	if got := readFile(t, filepath.Join(root, "index.php")); got != "current" {
		t.Fatalf("index.php = %q", got)
	}
	if _, err := os.Lstat(c.restoreJournalPath("shop")); !os.IsNotExist(err) {
		t.Fatalf("journal still there: %v", err)
	}
}

func TestRecoverRestoreNoJournals(t *testing.T) {
	c := restoreCore(t)
	if sites, err := c.RecoverRestores(context.Background()); err != nil || sites != nil {
		t.Fatalf("RecoverRestores = %v, %v", sites, err)
	}
}

func TestRecoverRestoreBadJournal(t *testing.T) {
	c := restoreCore(t)
	writeFile(t, c.restoreJournalPath("shop"), "{")
	if _, err := c.RecoverRestores(context.Background()); err == nil {
		t.Fatal("a broken journal was ignored")
	}
}

// Snapshots leave out .git and node_modules at any depth, and a restore
// brings them back from the folder moved aside, where their parent still
// exists.
func TestCloneTreeAndMoveSkipped(t *testing.T) {
	base := t.TempDir()
	src := filepath.Join(base, "my shop")
	writeFile(t, filepath.Join(src, "index.php"), "v1")
	writeFile(t, filepath.Join(src, "wp-content", "themes", "t", "style.css"), "css")
	writeFile(t, filepath.Join(src, ".git", "HEAD"), "ref")
	writeFile(t, filepath.Join(src, "wp-content", "themes", "t", "node_modules", "x.js"), "js")
	writeFile(t, filepath.Join(src, "front", ".next", "cache", "x"), "built")
	writeFile(t, filepath.Join(src, ".venv", "bin", "python"), "py")

	snap := filepath.Join(base, "snapshot", "files")
	if err := os.MkdirAll(filepath.Dir(snap), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := cloneTree(src, snap); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(snap, "wp-content", "themes", "t", "style.css")); got != "css" {
		t.Fatalf("style.css = %q", got)
	}
	for _, skipped := range []string{".git", "wp-content/themes/t/node_modules", "front/.next", ".venv"} {
		if _, err := os.Lstat(filepath.Join(snap, skipped)); !os.IsNotExist(err) {
			t.Errorf("%s saved in the snapshot: %v", skipped, err)
		}
	}

	// The site changes, then the snapshot is restored.
	writeFile(t, filepath.Join(src, "index.php"), "v2")
	aside := src + ".benbow-restore-1"
	if err := os.Rename(src, aside); err != nil {
		t.Fatal(err)
	}
	if err := cloneTree(snap, src); err != nil {
		t.Fatal(err)
	}
	if err := moveSkipped(aside, src); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(src, "index.php")); got != "v1" {
		t.Fatalf("index.php = %q, want the snapshot's", got)
	}
	if got := readFile(t, filepath.Join(src, ".git", "HEAD")); got != "ref" {
		t.Fatalf(".git not moved back: %q", got)
	}
	if got := readFile(t, filepath.Join(src, "wp-content", "themes", "t", "node_modules", "x.js")); got != "js" {
		t.Fatalf("node_modules not moved back: %q", got)
	}
}
