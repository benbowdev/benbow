// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/store"
)

func TestRiskyAgentCall(t *testing.T) {
	tests := []struct {
		tool string
		args string
		want bool
	}{
		{ToolWP, "plugin install woocommerce --activate", true},
		{ToolWP, "plugin list", false},
		{ToolWP, "theme update --all", true},
		{ToolWP, "core update", true},
		{ToolWP, "core version", false},
		{ToolWP, "language core install it_IT", true},
		{ToolWP, "db import dump.sql", true},
		{ToolWP, "db query SELECT 1", true},
		{ToolWP, "db tables", false},
		{ToolWP, "search-replace a b", true},
		{ToolWP, "search-replace a b --dry-run", false},
		{ToolWP, "option update home https://x", true},
		{ToolWP, "option update blogname Hi", false},
		{ToolWP, "post create --post_title=x", false},
		{ToolArtisan, "migrate", true},
		{ToolArtisan, "migrate:fresh --seed", true},
		{ToolArtisan, "migrate:status", false},
		{ToolArtisan, "db:seed", true},
		{ToolArtisan, "route:list", false},
		{ToolArtisan, "schema:dump --prune", true},
		{ToolComposer, "require laravel/sanctum", true},
		{ToolComposer, "show", false},
	}
	for _, tt := range tests {
		if got := RiskyAgentCall(tt.tool, strings.Fields(tt.args)); got != tt.want {
			t.Errorf("%s %s = %v, want %v", tt.tool, tt.args, got, tt.want)
		}
	}
}

func sessionCore(t *testing.T) (*Core, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "covo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	c := &Core{Store: st, Layout: paths.Layout{Lock: filepath.Join(dir, "covo.lock"), Data: filepath.Join(dir, "data"), Tmp: filepath.Join(dir, "tmp")}}
	site := filepath.Join(dir, "site")
	for _, d := range []string{site, filepath.Join(site, ".git")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(site, "index.php"), []byte("v1"), 0o644)
	_ = os.WriteFile(filepath.Join(site, ".git", "HEAD"), []byte("main"), 0o644)
	if err := st.AddSite(context.Background(), store.Site{Name: "shop", Path: site, PHPMinor: "8.4", Domain: "shop.localhost", Enabled: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return c, site
}

func TestAgentSession(t *testing.T) {
	ctx := context.Background()
	c, site := sessionCore(t)
	if err := c.SetSnapshotKeep(ctx, 2); err != nil {
		t.Fatal(err)
	}

	s, opened, err := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Agent: "claude-code", Source: SessionFromFolder})
	if err != nil || !opened || !s.DBSaved || s.BaseSnapshot == "" {
		t.Fatalf("open = %+v, %v, %v", s, opened, err)
	}
	again, opened, err := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Source: SessionFromCall})
	if err != nil || opened || again.ID != s.ID {
		t.Fatalf("second ensure = %+v, %v, %v", again, opened, err)
	}

	// Many snapshots later the starting point is still there; checkpoints
	// are not counted in keep and are capped per session.
	for i := range maxSessionCheckpoints + 3 {
		if _, err := c.Checkpoint(ctx, "shop", "cp"); err != nil {
			t.Fatalf("checkpoint %d: %v", i, err)
		}
	}
	for range 4 {
		if _, err := c.CreateSnapshot(ctx, "shop", SnapshotManual, ""); err != nil {
			t.Fatal(err)
		}
	}
	info, err := c.AgentSession(ctx, "shop")
	if err != nil || info.Base == nil || len(info.Checkpoints) != maxSessionCheckpoints {
		t.Fatalf("info = base %v, %d checkpoints, %v", info.Base, len(info.Checkpoints), err)
	}

	// An edit made outside Covo, then undo everything.
	_ = os.WriteFile(filepath.Join(site, "index.php"), []byte("changed by the agent"), 0o644)
	_ = os.WriteFile(filepath.Join(site, "new.php"), []byte("x"), 0o644)
	_ = os.WriteFile(filepath.Join(site, ".git", "HEAD"), []byte("feature"), 0o644)
	res, err := c.CloseAgentSession(ctx, "shop", SessionRevert)
	if err != nil || res.Restored == nil {
		t.Fatalf("revert = %+v, %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(site, "index.php")); string(b) != "v1" {
		t.Fatalf("index.php after revert = %q", b)
	}
	if _, err := os.Stat(filepath.Join(site, "new.php")); !os.IsNotExist(err) {
		t.Fatal("new.php survived the revert")
	}
	if b, _ := os.ReadFile(filepath.Join(site, ".git", "HEAD")); string(b) != "feature" {
		t.Fatalf(".git was touched: %q", b)
	}
	if _, err := c.AgentSession(ctx, "shop"); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("session still open: %v", err)
	}
	snaps, _ := c.ListSnapshots(ctx, "shop")
	for _, sn := range snaps {
		if sn.Kind == SnapshotCheckpoint {
			t.Fatalf("checkpoint left after closing: %+v", sn)
		}
	}
	if len(snaps) != 2 {
		t.Fatalf("after closing the keep limit applies again: %d snapshots", len(snaps))
	}

	// Keep closes without touching files.
	if _, _, err := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Source: SessionFromCall}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(site, "index.php"), []byte("v2"), 0o644)
	if _, err := c.CloseAgentSession(ctx, "shop", SessionKeep); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(site, "index.php")); string(b) != "v2" {
		t.Fatalf("keep changed files: %q", b)
	}
}

func TestExpireAgentSessions(t *testing.T) {
	ctx := context.Background()
	c, _ := sessionCore(t)
	s, _, err := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Source: SessionFromCall})
	if err != nil {
		t.Fatal(err)
	}
	if closed, _ := c.ExpireAgentSessions(ctx); len(closed) != 0 {
		t.Fatalf("fresh session expired: %v", closed)
	}
	_ = c.Store.TouchAgentSession(ctx, s.ID, time.Now().Add(-25*time.Hour))
	if closed, _ := c.ExpireAgentSessions(ctx); len(closed) != 1 {
		t.Fatalf("idle session not expired: %v", closed)
	}
	got, _ := c.Store.AgentSession(ctx, s.ID)
	if got.Outcome != SessionTimeout || got.ClosedAt == nil {
		t.Fatalf("expired session = %+v", got)
	}
	if err := c.SetSessionIdleHours(ctx, 0); err != nil {
		t.Fatal(err)
	}
	s2, _, _ := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Source: SessionFromCall})
	_ = c.Store.TouchAgentSession(ctx, s2.ID, time.Now().Add(-1000*time.Hour))
	if closed, _ := c.ExpireAgentSessions(ctx); len(closed) != 0 {
		t.Fatalf("idle 0 means never: %v", closed)
	}
}

func TestSiteForFolder(t *testing.T) {
	ctx := context.Background()
	c, site := sessionCore(t)
	sub := filepath.Join(site, "wp-content")
	_ = os.MkdirAll(sub, 0o755)
	for dir, want := range map[string]string{site: "shop", sub: "shop", filepath.Dir(site): "", site + "-other": ""} {
		if got, err := c.SiteForFolder(ctx, dir); err != nil || got != want {
			t.Errorf("SiteForFolder(%s) = %q, %v; want %q", dir, got, err, want)
		}
	}
}
