// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
)

func groupsCore(t *testing.T) *Core {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "benbow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Core{Store: st, Layout: paths.Layout{Lock: filepath.Join(dir, "benbow.lock")}}
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
	addSite := func(n string) {
		s := store.Site{Name: n, Path: "/x", PHPMinor: "8.4", Domain: n + ".localhost", CreatedAt: time.Now()}
		if err := c.Store.AddSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"b-site", "a-site", "c-site"} {
		addSite(n)
	}
	g := func(id int64) string { return "group:" + strconv.FormatInt(id, 10) }
	level := func(parent int64) []string {
		gs, _ := c.Store.ListGroups(ctx)
		ss, _ := c.Store.ListSites(ctx)
		return levelOrder(gs, ss, parent)
	}
	// Never ordered: groups then sites, by name.
	if got := level(0); !slices.Equal(got, []string{g(acme), g(beta), g(zed), "site:a-site", "site:b-site", "site:c-site"}) {
		t.Fatalf("default = %v", got)
	}

	// Mixed, partial: the rest follows in its current order.
	res, err := c.SetListOrder(ctx, ListOrder{Items: []string{"site:c-site", g(zed), "site:a-site"}})
	want := []string{"site:c-site", g(zed), "site:a-site", g(acme), g(beta), "site:b-site"}
	if err != nil || !slices.Equal(res.Items, want) {
		t.Fatalf("order = %+v, %v", res, err)
	}
	if got := level(0); !slices.Equal(got, want) {
		t.Fatalf("level = %v", got)
	}
	// Groups listed in their own order, each followed by its subgroups.
	gs, _ := c.ListGroups(ctx)
	var paths []string
	for _, x := range gs {
		paths = append(paths, x.Path)
	}
	if !slices.Equal(paths, []string{"Zed", "Zed/Inner", "Acme", "Beta"}) {
		t.Fatalf("groups = %v", paths)
	}

	// New items go last once a level is ordered (sites and groups alike).
	addSite("aaa")
	add("Aaa")
	if got := level(0); !slices.Equal(got[len(got)-2:], []string{"site:aaa", got[len(got)-1]}) || !strings.HasPrefix(got[len(got)-1], "group:") {
		t.Fatalf("new items not last: %v", got)
	}

	// Moving into a group with a position.
	if _, err := c.SetListOrder(ctx, ListOrder{Group: zed, Items: []string{"site:b-site", g(acme)}}); err != nil {
		t.Fatal(err)
	}
	if got := level(zed); !slices.Equal(got, []string{"site:b-site", g(acme), g(inner)}) {
		t.Fatalf("zed = %v", got)
	}

	for _, tt := range []struct {
		o    ListOrder
		code string
	}{
		{ListOrder{Group: inner, Items: []string{g(zed)}}, errcode.InvalidInput}, // inside itself
		{ListOrder{Group: 999}, errcode.NotFound},
		{ListOrder{Items: []string{"group:999"}}, errcode.NotFound},
		{ListOrder{Items: []string{"site:nope"}}, errcode.NotFound},
		{ListOrder{Items: []string{"shop"}}, errcode.InvalidInput},
	} {
		if _, err := c.SetListOrder(ctx, tt.o); errcode.Code(err) != tt.code {
			t.Errorf("%+v err = %v, want %s", tt.o, err, tt.code)
		}
	}
	// Same name in the target level.
	if _, err := c.AddGroupPath(ctx, "Beta/Acme"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetListOrder(ctx, ListOrder{Group: beta, Items: []string{g(acme)}}); errcode.Code(err) != errcode.Conflict {
		t.Errorf("name clash err = %v", err)
	}
}
