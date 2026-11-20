// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/importers"
	"github.com/benbowdev/benbow/internal/store"
)

// Issue #1: Local names every site's database "local", and the second
// imported site was linked to the first one's database.
func TestMigrationDBName(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	add := func(name, db, engine string) {
		t.Helper()
		if err := c.Store.AddSite(ctx, store.Site{Name: name, Path: t.TempDir(), PHPMinor: "8.4", Domain: name + ".localhost", Database: db, DBEngine: engine}); err != nil {
			t.Fatal(err)
		}
	}
	add("site-a", "local", EngineMySQL)
	add("site-b", "", "")
	add("old", "wordpress", EngineMySQL)
	add("taken", "shop", EngineMySQL)
	add("shop", "", "")
	add("pg", "blog", EnginePostgres)
	add("blog", "", "")

	for _, tt := range []struct {
		source, site, srcDB, want string
	}{
		{importers.Local, "site-b", "local", "site_b"},       // Local: always the site's name
		{importers.Local, "site-a", "local", "site_a"},       // even the first one
		{importers.MAMP, "site-b", "client_db", "client_db"}, // MAMP keeps unique names
		{importers.MAMP, "site-b", "wordpress", "site_b"},    // taken by "old"
		{importers.Herd, "shop", "shop", "shop_2"},           // source name and site name both taken by "taken"
		{importers.MAMP, "blog", "blog", "blog"},             // a PostgreSQL database of the same name does not count
		{importers.MAMP, "site-b", "bad name;", "site_b"},    // not a valid MySQL name
		{importers.MAMP, "site-a", "local", "local"},         // its own database: kept (re-import)
	} {
		site, _ := c.Store.Site(ctx, tt.site)
		got, err := c.migrationDBName(ctx, tt.source, site, projectDB{db: tt.srcDB})
		if err != nil || got != tt.want {
			t.Errorf("%s %s %q = %q, %v (want %q)", tt.source, tt.site, tt.srcDB, got, err, tt.want)
		}
	}
}

func TestSharedDatabaseRefused(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	for _, s := range []store.Site{
		{Name: "a", Path: t.TempDir(), PHPMinor: "8.4", Domain: "a.localhost", Database: "local", DBEngine: EngineMySQL},
		{Name: "b", Path: t.TempDir(), PHPMinor: "8.4", Domain: "b.localhost", Database: "local", DBEngine: EngineMySQL},
		{Name: "c", Path: t.TempDir(), PHPMinor: "8.4", Domain: "c.localhost"},
	} {
		if err := c.Store.AddSite(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	others, err := c.sitesUsingDatabase(ctx, "a", "local", EngineMySQL)
	if err != nil || len(others) != 1 || others[0] != "b" {
		t.Fatalf("others = %v, %v", others, err)
	}
	if _, err := c.LinkSiteDatabaseOn(ctx, "c", "local", EngineMySQL, false); errcode.Code(err) != errcode.Conflict {
		t.Errorf("link to a used database = %v", err)
	}
	// Deleting a database another site uses is refused before anything.
	if _, err := c.UnlinkSiteDatabase(ctx, "a", true); errcode.Code(err) != errcode.Conflict {
		t.Errorf("drop a shared database = %v", err)
	}
	// Unlinking without deleting is fine.
	if _, err := c.UnlinkSiteDatabase(ctx, "a", false); err != nil {
		t.Fatal(err)
	}
	if s, _ := c.Store.Site(ctx, "a"); s.Database != "" {
		t.Fatalf("a still has %q", s.Database)
	}
}
