// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bytes"
	"compress/gzip"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
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

// A restore killed while deleting the old folder had already finished:
// recovery deletes what is left of the old folder and keeps the restored
// one (it used to bring the half-deleted folder back).
func TestRecoverRestoreDone(t *testing.T) {
	c := restoreCore(t)
	root := filepath.Join(t.TempDir(), "shop")
	aside := root + ".benbow-restore-1"
	writeFile(t, filepath.Join(root, "index.php"), "restored")
	writeFile(t, filepath.Join(aside, "wp-content", "left.php"), "half deleted")
	j := restoreJournal{Site: "shop", Root: root, Aside: aside, DBEngine: "mysql", DBName: "shop", DBFile: "/nonexistent.sql.gz", Done: true}
	if err := c.writeRestoreJournal(j); err != nil {
		t.Fatal(err)
	}
	sites, err := c.RecoverRestores(context.Background())
	if err != nil || len(sites) != 0 {
		t.Fatalf("RecoverRestores = %v, %v", sites, err)
	}
	if got := readFile(t, filepath.Join(root, "index.php")); got != "restored" {
		t.Fatalf("index.php = %q", got)
	}
	for _, p := range []string{aside, c.restoreJournalPath("shop")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still there: %v", p, err)
		}
	}
}

// A snapshot with missing files or a damaged dump is refused before the
// site is touched.
func TestCheckSnapshot(t *testing.T) {
	dir := t.TempDir()
	files := filepath.Join(dir, "files")
	writeFile(t, filepath.Join(files, "index.php"), "x")

	var dump bytes.Buffer
	gz := gzip.NewWriter(&dump)
	_, _ = gz.Write(bytes.Repeat([]byte("INSERT INTO wp_options VALUES (1);\n"), 2000))
	_ = gz.Close()
	good := filepath.Join(dir, "good.sql.gz")
	cut := filepath.Join(dir, "cut.sql.gz")
	writeFile(t, good, dump.String())
	writeFile(t, cut, dump.String()[:dump.Len()/2])
	pg := filepath.Join(dir, "database.pgdump")
	writeFile(t, pg, "PGDMP\x01\x0e")
	notPG := filepath.Join(dir, "other.pgdump")
	writeFile(t, notPG, "-- plain SQL")

	for _, tc := range []struct {
		name string
		sn   store.Snapshot
		ok   bool
	}{
		{"files only", store.Snapshot{FilesDir: files}, true},
		{"mysql", store.Snapshot{FilesDir: files, DBEngine: "mysql", DBFile: good}, true},
		{"postgres", store.Snapshot{FilesDir: files, DBEngine: EnginePostgres, DBFile: pg}, true},
		{"files missing", store.Snapshot{FilesDir: filepath.Join(dir, "gone")}, false},
		{"dump missing", store.Snapshot{FilesDir: files, DBEngine: "mysql", DBFile: filepath.Join(dir, "gone.sql.gz")}, false},
		{"dump truncated", store.Snapshot{FilesDir: files, DBEngine: "mysql", DBFile: cut}, false},
		{"not a pg archive", store.Snapshot{FilesDir: files, DBEngine: EnginePostgres, DBFile: notPG}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkSnapshot(tc.sn)
			if (err == nil) != tc.ok {
				t.Fatalf("checkSnapshot = %v, want ok %v", err, tc.ok)
			}
		})
	}
}

// The file list saved with a snapshot catches files lost or changed in the
// snapshot's folder; snapshots without a list (older ones) pass.
func TestSnapshotFileList(t *testing.T) {
	dir := t.TempDir()
	sn := store.Snapshot{FilesDir: filepath.Join(dir, "files")}
	writeFile(t, filepath.Join(sn.FilesDir, "index.php"), "x")
	writeFile(t, filepath.Join(sn.FilesDir, "wp-content", "plugins", "p\tq", "p.php"), "plugin")
	if err := os.Symlink("index.php", filepath.Join(sn.FilesDir, "link.php")); err != nil {
		t.Fatal(err)
	}
	if err := checkSnapshot(sn); err != nil {
		t.Fatalf("no list yet: %v", err)
	}
	if err := writeFileList(sn.FilesDir, fileListPath(sn)); err != nil {
		t.Fatal(err)
	}
	if err := checkSnapshot(sn); err != nil {
		t.Fatalf("whole snapshot refused: %v", err)
	}
	writeFile(t, filepath.Join(sn.FilesDir, "wp-content", "plugins", "p\tq", "p.php"), "plugin, changed")
	if err := checkSnapshot(sn); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("changed file: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(sn.FilesDir, "wp-content")); err != nil {
		t.Fatal(err)
	}
	if err := checkSnapshot(sn); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing file: %v", err)
	}
}
