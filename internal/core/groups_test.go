// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/store"
)

func groupsCore(t *testing.T) *Core {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "covo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Core{Store: st, Layout: paths.Layout{Lock: filepath.Join(dir, "covo.lock")}}
}

func TestGroups(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)

	acme, err := c.AddGroupPath(ctx, " Clients / Acme ")
	if err != nil || acme.Path != "Clients/Acme" {
		t.Fatalf("add path = %+v, %v", acme, err)
	}
	again, err := c.AddGroupPath(ctx, "clients/acme")
	if err != nil || again.ID != acme.ID {
		t.Fatalf("add existing path = %+v, %v (want id %d)", again, err, acme.ID)
	}
	clients, err := c.ResolveGroup(ctx, "/Clients/")
	if err != nil || clients != acme.ParentID {
		t.Fatalf("resolve = %d, %v", clients, err)
	}
	for _, tt := range []struct {
		name   string
		parent int64
		code   string
	}{
		{"acme", clients, errcode.Conflict},
		{"a/b", 0, errcode.InvalidInput},
		{"  ", 0, errcode.InvalidInput},
		{"Other", 999, errcode.NotFound},
	} {
		if _, err := c.AddGroup(ctx, tt.name, tt.parent); errcode.Code(err) != tt.code {
			t.Errorf("add %q in %d err = %v, want %s", tt.name, tt.parent, err, tt.code)
		}
	}

	// A group cannot go inside itself or its subgroups.
	for _, into := range []int64{clients, acme.ID} {
		if _, err := c.UpdateGroup(ctx, UpdateGroupRequest{ID: clients, Parent: &into}); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("move into %d err = %v", into, err)
		}
	}
	top := int64(0)
	moved, err := c.UpdateGroup(ctx, UpdateGroupRequest{ID: acme.ID, Name: "Acme Corp", Parent: &top})
	if err != nil || moved.Path != "Acme Corp" {
		t.Fatalf("move to top = %+v, %v", moved, err)
	}
	back := clients
	if _, err := c.UpdateGroup(ctx, UpdateGroupRequest{ID: acme.ID, Parent: &back}); err != nil {
		t.Fatal(err)
	}

	// Removing a group moves its sites and subgroups up one level.
	if err := c.Store.AddSite(ctx, store.Site{Name: "demo", Path: "/x", PHPMinor: "8.4", Domain: "demo.localhost",
		GroupID: acme.ID, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveGroup(ctx, clients); err != nil {
		t.Fatal(err)
	}
	all, err := c.ListGroups(ctx)
	if err != nil || len(all) != 1 || all[0].Path != "Acme Corp" || all[0].ParentID != 0 {
		t.Fatalf("after remove = %+v, %v", all, err)
	}
	if _, err := c.RemoveGroup(ctx, acme.ID); err != nil {
		t.Fatal(err)
	}
	site, err := c.Store.Site(ctx, "demo")
	if err != nil || site.GroupID != 0 {
		t.Fatalf("site after removing its group = %+v, %v", site, err)
	}
}

func TestGroupDepth(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	var parent int64
	for i := range maxGroupDepth {
		g, err := c.AddGroup(ctx, "level", parent)
		if err != nil {
			t.Fatalf("level %d: %v", i+1, err)
		}
		parent = g.ID
	}
	if _, err := c.AddGroup(ctx, "too deep", parent); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("too deep err = %v", err)
	}
}
