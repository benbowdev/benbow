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

func TestSnapshotUsageAndPrune(t *testing.T) {
	ctx := context.Background()
	c := runCore(t)
	for _, s := range []string{"shop", "blog"} {
		if err := c.Store.AddSite(ctx, store.Site{Name: s, Path: "/x/" + s, PHPMinor: "8.4", Domain: s + ".localhost", CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	add := func(site, id string, age time.Duration, size int) {
		dir := filepath.Join(c.snapshotDir(site), id)
		files := filepath.Join(dir, "files")
		if err := os.MkdirAll(files, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "database.sql.gz"), make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
		sn := store.Snapshot{ID: id, Site: site, CreatedAt: time.Now().Add(-age), Kind: SnapshotManual, FilesDir: files, Root: "/x/" + site}
		if err := c.Store.AddSnapshot(ctx, sn); err != nil {
			t.Fatal(err)
		}
	}
	add("shop", "old", 40*24*time.Hour, 100000)
	add("shop", "new", time.Hour, 50000)
	add("blog", "old", 60*24*time.Hour, 200000)

	all, err := c.SnapshotUsage(ctx, "")
	if err != nil || len(all.Snapshots) != 3 || all.Private < 350000 || all.Snapshots[0].ID != "new" {
		t.Fatalf("usage = %+v, %v", all, err)
	}
	one, err := c.SnapshotUsage(ctx, "shop")
	if err != nil || len(one.Snapshots) != 2 {
		t.Fatalf("shop usage = %+v, %v", one, err)
	}

	dry, err := c.PruneSnapshots(ctx, PruneRequest{OlderThan: 30 * 24 * time.Hour, DryRun: true})
	if err != nil || len(dry.Removed) != 2 || dry.Freed < 300000 {
		t.Fatalf("dry run = %+v, %v", dry, err)
	}
	if left, _ := c.SnapshotUsage(ctx, ""); len(left.Snapshots) != 3 {
		t.Fatal("a dry run deleted snapshots")
	}
	res, err := c.PruneSnapshots(ctx, PruneRequest{Site: "shop", OlderThan: 30 * 24 * time.Hour})
	if err != nil || len(res.Removed) != 1 || res.Removed[0].Site != "shop" {
		t.Fatalf("prune shop = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(c.snapshotDir("shop"), "old")); err == nil {
		t.Error("the pruned snapshot's folder is still there")
	}
	if left, _ := c.SnapshotUsage(ctx, ""); len(left.Snapshots) != 2 {
		t.Errorf("left = %+v", left)
	}
	if _, err := c.PruneSnapshots(ctx, PruneRequest{}); err == nil {
		t.Error("prune without an age accepted")
	}
}
