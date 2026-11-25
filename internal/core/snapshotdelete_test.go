// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/store"
)

func TestDeleteSnapshot(t *testing.T) {
	ctx := context.Background()
	c := runCore(t)
	if err := c.Store.AddSite(ctx, store.Site{Name: "shop", Path: "/x/shop", PHPMinor: "8.4", Domain: "shop.localhost", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	add := func(id string) string {
		dir := filepath.Join(c.snapshotDir("shop"), id)
		files := filepath.Join(dir, "files")
		// A read-only folder, as a project can have: deleting in place
		// failed on it.
		locked := filepath.Join(files, "vendor", "locked")
		if err := os.MkdirAll(locked, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(locked, "a.php"), []byte("<?php"), 0o400); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(locked, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(locked, 0o700) })
		if err := c.Store.AddSnapshot(ctx, store.Snapshot{ID: id, Site: "shop", CreatedAt: time.Now(), Kind: SnapshotManual, FilesDir: files, Root: "/x/shop"}); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	dir := add("one")
	if err := c.DeleteSnapshot(ctx, "shop", "one"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the snapshot folder is still there: %v", err)
	}
	if left, _ := os.ReadDir(c.snapshotTrash()); len(left) != 0 {
		t.Errorf("the trash still holds %d entries", len(left))
	}
	if _, err := c.Store.Snapshot(ctx, "shop", "one"); err == nil {
		t.Error("the deleted snapshot is still listed")
	}

	// A folder already gone: the snapshot is forgotten all the same.
	dir = add("two")
	if err := os.Chmod(filepath.Join(dir, "files", "vendor", "locked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteSnapshot(ctx, "shop", "two"); err != nil {
		t.Fatalf("delete without a folder: %v", err)
	}

	// Leftovers in the trash go when the daemon empties it.
	stale := filepath.Join(c.snapshotTrash(), "shop-old", "files")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.EmptySnapshotTrash(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.snapshotTrash()); !os.IsNotExist(err) {
		t.Errorf("trash not emptied: %v", err)
	}
}
