// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

func TestRenameSite(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	root := filepath.Dir(c.Layout.Lock)
	c.Layout.Logs, c.Layout.Data = filepath.Join(root, "logs"), filepath.Join(root, "data")
	for _, n := range []string{"shop", "blog"} {
		s := store.Site{Name: n, Path: "/x", PHPMinor: "8.4", Domain: n + ".localhost", CreatedAt: time.Now()}
		if err := c.Store.AddSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Store.SetAliases(ctx, "shop", []string{"www.shop.localhost"}); err != nil {
		t.Fatal(err)
	}
	snapFiles := filepath.Join(c.snapshotDir("shop"), "s1", "files")
	if err := os.MkdirAll(snapFiles, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(c.siteLogDir("shop"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.AddSnapshot(ctx, store.Snapshot{ID: "s1", Site: "shop", CreatedAt: time.Now(), Kind: "manual", FilesDir: snapFiles}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.SetMCPPermission(ctx, "shop", true); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		to   string
		code string
	}{
		{"Bad Name!", errcode.InvalidInput},
		{"shop", errcode.InvalidInput},
		{"blog", errcode.Conflict},
	} {
		if _, err := c.RenameSite(ctx, RenameSiteRequest{Name: "shop", NewName: tt.to}); errcode.Code(err) != tt.code {
			t.Errorf("rename to %q err = %v, want %s", tt.to, err, tt.code)
		}
	}
	if _, err := c.RenameSite(ctx, RenameSiteRequest{Name: "nope", NewName: "x"}); errcode.Code(err) != errcode.NotFound {
		t.Errorf("missing site err = %v", err)
	}

	res, err := c.RenameSite(ctx, RenameSiteRequest{Name: "shop", NewName: " Store "})
	if err != nil || res.Name != "store" || res.Domain != "shop.localhost" {
		t.Fatalf("rename = %+v, %v", res.Site, err)
	}
	if _, err := c.Store.Site(ctx, "shop"); errcode.Code(err) != errcode.NotFound {
		t.Errorf("old name still there: %v", err)
	}
	if res.Aliases == nil || res.Aliases[0] != "www.shop.localhost" {
		t.Errorf("aliases = %v", res.Aliases)
	}
	snaps, err := c.Store.ListSnapshots(ctx, "store")
	want := filepath.Join(c.snapshotDir("store"), "s1", "files")
	if err != nil || len(snaps) != 1 || snaps[0].FilesDir != want {
		t.Fatalf("snapshots = %+v, %v (want files in %s)", snaps, err, want)
	}
	for _, d := range []string{want, c.siteLogDir("store")} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s not moved: %v", d, err)
		}
	}
	if _, err := os.Stat(c.snapshotDir("shop")); !os.IsNotExist(err) {
		t.Errorf("old snapshot folder still there: %v", err)
	}
	perms, _ := c.Store.MCPPermissions(ctx)
	if !perms["store"] || perms["shop"] {
		t.Errorf("MCP permissions = %v", perms)
	}
}
