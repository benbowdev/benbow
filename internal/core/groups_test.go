// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"path/filepath"
	"slices"
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

func TestSetGroupEnabled(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	acme, err := c.AddGroupPath(ctx, "Clients/Acme")
	if err != nil {
		t.Fatal(err)
	}
	other, err := c.AddGroupPath(ctx, "Other")
	if err != nil {
		t.Fatal(err)
	}
	clients := acme.ParentID
	for _, s := range []store.Site{
		{Name: "top", GroupID: clients, Enabled: true},
		{Name: "deep", GroupID: acme.ID, Enabled: true},
		{Name: "old", GroupID: acme.ID, Archived: true},
		{Name: "outside", GroupID: other.ID, Enabled: true},
		{Name: "loose", Enabled: true},
	} {
		s.Path, s.PHPMinor, s.Domain, s.CreatedAt = "/x", "8.4", s.Name+".localhost", time.Now()
		if err := c.Store.AddSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	enabled := func() map[string]bool {
		sites, _ := c.Store.ListSites(ctx)
		m := map[string]bool{}
		for _, s := range sites {
			m[s.Name] = s.Enabled
		}
		return m
	}

	res, err := c.SetGroupEnabled(ctx, clients, false)
	if err != nil || len(res.Changed) != 2 || len(res.Archived) != 1 || res.Group.Path != "Clients" {
		t.Fatalf("off = %+v, %v", res, err)
	}
	if m := enabled(); m["top"] || m["deep"] || !m["outside"] || !m["loose"] || m["old"] {
		t.Fatalf("after off = %v", m)
	}
	if res, err = c.SetGroupEnabled(ctx, acme.ID, true); err != nil || len(res.Changed) != 1 || res.Changed[0] != "deep" {
		t.Fatalf("on = %+v, %v", res, err)
	}
	if m := enabled(); m["top"] || !m["deep"] || m["old"] {
		t.Fatalf("after on = %v", m)
	}
	if _, err := c.SetGroupEnabled(ctx, 999, true); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("missing group err = %v", err)
	}
}

func TestSetListOrder(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	add := func(path string) int64 {
		g, err := c.AddGroupPath(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		return g.ID
	}
	beta, acme, zed := add("Beta"), add("Acme"), add("Zed")
	inner := add("Zed/Inner")
	for _, n := range []string{"b-site", "a-site", "c-site"} {
		s := store.Site{Name: n, Path: "/x", PHPMinor: "8.4", Domain: n + ".localhost", CreatedAt: time.Now()}
		if err := c.Store.AddSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	groupOrder := func() []string {
		gs, err := c.ListGroups(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, g := range gs {
			out = append(out, g.Path)
		}
		return out
	}
	siteOrder := func(group int64) []string {
		ss, _ := c.Store.ListSites(ctx)
		var out []string
		for _, s := range ss {
			if s.GroupID == group {
				out = append(out, s.Name)
			}
		}
		return out
	}
	// Never ordered: by name, each group followed by its subgroups.
	if got := groupOrder(); !slices.Equal(got, []string{"Acme", "Beta", "Zed", "Zed/Inner"}) {
		t.Fatalf("default groups = %v", got)
	}
	if got := siteOrder(0); !slices.Equal(got, []string{"a-site", "b-site", "c-site"}) {
		t.Fatalf("default sites = %v", got)
	}

	// Partial lists: the rest follows in its current order.
	res, err := c.SetListOrder(ctx, ListOrder{Groups: []int64{zed}, Sites: []string{"c-site", "a-site"}})
	if err != nil || !slices.Equal(res.Groups, []int64{zed, acme, beta}) {
		t.Fatalf("order = %+v, %v", res, err)
	}
	if got := groupOrder(); !slices.Equal(got, []string{"Zed", "Zed/Inner", "Acme", "Beta"}) {
		t.Fatalf("groups = %v", got)
	}
	if got := siteOrder(0); !slices.Equal(got, []string{"c-site", "a-site", "b-site"}) {
		t.Fatalf("sites = %v", got)
	}

	// New items go last once a level is ordered.
	if err := c.Store.AddSite(ctx, store.Site{Name: "aaa", Path: "/x", PHPMinor: "8.4", Domain: "aaa.localhost", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if got := siteOrder(0); got[len(got)-1] != "aaa" {
		t.Fatalf("new site not last: %v", got)
	}

	// Moving into a group with a position.
	if _, err := c.SetListOrder(ctx, ListOrder{Group: zed, Groups: []int64{acme, inner}, Sites: []string{"b-site"}}); err != nil {
		t.Fatal(err)
	}
	if got := groupOrder(); !slices.Equal(got, []string{"Zed", "Zed/Acme", "Zed/Inner", "Beta"}) {
		t.Fatalf("after move = %v", got)
	}
	if got := siteOrder(zed); !slices.Equal(got, []string{"b-site"}) {
		t.Fatalf("zed sites = %v", got)
	}

	for _, tt := range []struct {
		o    ListOrder
		code string
	}{
		{ListOrder{Group: inner, Groups: []int64{zed}}, errcode.InvalidInput}, // inside itself
		{ListOrder{Group: 999}, errcode.NotFound},
		{ListOrder{Groups: []int64{999}}, errcode.NotFound},
		{ListOrder{Sites: []string{"nope"}}, errcode.NotFound},
	} {
		if _, err := c.SetListOrder(ctx, tt.o); errcode.Code(err) != tt.code {
			t.Errorf("%+v err = %v, want %s", tt.o, err, tt.code)
		}
	}
	// Same name in the target level.
	if _, err := c.AddGroupPath(ctx, "Beta/Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetListOrder(ctx, ListOrder{Group: beta, Groups: []int64{acme}}); errcode.Code(err) != errcode.Conflict {
		t.Errorf("name clash err = %v", err)
	}
}
