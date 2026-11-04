// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

func openTest(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Covo Home", "covo.db")
	if err := mkdirAll(filepath.Dir(path)); err != nil {
		t.Fatal(err)
	}
	s, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, path
}

func TestMigrateIsIdempotent(t *testing.T) {
	s, path := openTest(t)
	_ = s.Close()
	s2, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	_ = s2.Close()
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if _, ok, err := s.Setting(ctx, "https_port"); err != nil || ok {
		t.Fatalf("unset setting: ok=%v err=%v", ok, err)
	}
	for _, v := range []string{"8443", "9443"} {
		if err := s.SetSetting(ctx, "https_port", v); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.Setting(ctx, "https_port")
		if err != nil || !ok || got != v {
			t.Fatalf("Setting = %q, %v, %v; want %q", got, ok, err, v)
		}
	}
	if err := s.DeleteSetting(ctx, "https_port"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSetting(ctx, "https_port"); err != nil {
		t.Fatalf("delete twice: %v", err)
	}
}

func TestLatestPHP(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	for _, v := range []string{"8.4.2", "8.4.16", "8.2.32"} {
		if err := s.PutPHP(ctx, PHPVersion{Version: v, Minor: v[:3], Path: "/x/" + v, SHA256: "abc", InstalledAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		minor    string
		want     string
		wantCode string
	}{
		{"8.4", "8.4.16", ""},
		{"8.2", "8.2.32", ""},
		{"8.3", "", errcode.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.minor, func(t *testing.T) {
			got, err := s.LatestPHP(ctx, tt.minor)
			if tt.wantCode != "" {
				if errcode.Code(err) != tt.wantCode {
					t.Fatalf("err = %v, want code %s", err, tt.wantCode)
				}
				return
			}
			if err != nil || got.Version != tt.want {
				t.Fatalf("LatestPHP = %q, %v; want %q", got.Version, err, tt.want)
			}
		})
	}
}

func TestSites(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	demo := Site{Name: "demo", Path: "/Users/a/My Sites/demo", PHPMinor: "8.4", Domain: "demo.localhost", HTTPS: true, Enabled: true, CreatedAt: time.Now()}
	if err := s.AddSite(ctx, demo); err != nil {
		t.Fatal(err)
	}
	if err := s.AddSite(ctx, demo); errcode.Code(err) != errcode.Conflict {
		t.Fatalf("duplicate add err = %v, want conflict", err)
	}
	got, err := s.Site(ctx, "demo")
	if err != nil || got.Path != demo.Path || !got.HTTPS || !got.Enabled {
		t.Fatalf("Site = %+v, %v", got, err)
	}
	other := Site{Name: "other", Path: "/Users/a/other", PHPMinor: "8.2", Domain: "other.localhost", HTTPS: true, CreatedAt: time.Now()}
	if err := s.AddSite(ctx, other); err != nil {
		t.Fatal(err)
	}
	moved := demo
	moved.Path, moved.PHPMinor, moved.Domain, moved.Enabled = "/Users/a/new", "8.2", "demo.covo.test", false
	moved.Database, moved.DBUser = "demo_db", "demo"
	moved.PHPSettings = map[string]string{"memory_limit": "1G"}
	moved.Archived = true
	if err := s.UpdateSite(ctx, moved); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Site(ctx, "demo"); got.Path != moved.Path || got.PHPMinor != "8.2" || got.Domain != moved.Domain || got.Enabled || got.Database != "demo_db" || got.DBUser != "demo" || got.PHPSettings["memory_limit"] != "1G" || !got.Archived || !got.CreatedAt.Equal(demo.CreatedAt.Truncate(time.Second)) {
		t.Fatalf("after update Site = %+v", got)
	}
	moved.Domain = "other.localhost"
	if err := s.UpdateSite(ctx, moved); errcode.Code(err) != errcode.Conflict {
		t.Fatalf("update to a taken domain err = %v, want conflict", err)
	}
	if err := s.UpdateSite(ctx, Site{Name: "missing", Domain: "missing.localhost"}); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("update missing err = %v, want not_found", err)
	}
	if err := s.SetAliases(ctx, "demo", []string{"www.demo.localhost", "shop.localhost"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Site(ctx, "demo"); len(got.Aliases) != 2 || got.Aliases[0] != "shop.localhost" {
		t.Fatalf("aliases = %v", got.Aliases)
	}
	if err := s.SetAliases(ctx, "other", []string{"shop.localhost"}); errcode.Code(err) != errcode.Conflict {
		t.Fatalf("taken alias err = %v, want conflict", err)
	}
	if err := s.SetAliases(ctx, "missing", []string{"x.localhost"}); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("alias for missing site err = %v, want not_found", err)
	}
	if got, _ := s.Site(ctx, "other"); len(got.Aliases) != 0 || got.Aliases == nil {
		t.Fatalf("other aliases = %#v, want empty", got.Aliases)
	}
	if err := s.RemoveSite(ctx, "other"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSite(ctx, "demo"); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveSite(ctx, "demo"); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("second remove err = %v, want not_found", err)
	}
	sites, err := s.ListSites(ctx)
	if err != nil || len(sites) != 0 {
		t.Fatalf("ListSites = %v, %v", sites, err)
	}
	if a, err := s.aliases(ctx); err != nil || len(a) != 0 {
		t.Fatalf("aliases left after remove: %v, %v", a, err)
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		a, b string
		want int
	}{
		{"8.4.16", "8.4.2", 1},
		{"8.2.32", "8.4.0", -1},
		{"8.4", "8.4.0", 0},
	}
	for _, tt := range tests {
		if got := CompareVersions(tt.a, tt.b); got != tt.want {
			t.Errorf("CompareVersions(%q, %q) = %d, want %d", tt.a, tt.b, got, tt.want)
		}
	}
}

func mkdirAll(dir string) error { return os.MkdirAll(dir, 0o755) }

// Several covo processes may open a new database at once (the daemon, the
// CLI, an agent's MCP server): migrations must take turns.
func TestOpenConcurrently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "covo.db")
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			s, err := Open(context.Background(), path)
			if err == nil {
				err = s.Close()
			}
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Error(err)
		}
	}
}

// openAt opens a database migrated only up to version limit, as an older
// covo left it.
func openAt(t *testing.T, path string, limit int) *Store {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrateUpTo(context.Background(), limit); err != nil {
		t.Fatal(err)
	}
	return s
}

// A database with data from an older Covo (schema 0007: sites, aliases,
// groups) migrates to the current schema without losing anything, and the
// newer columns get their defaults.
func TestMigrateOldDatabaseWithData(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "covo.db")
	old := openAt(t, path, 7)
	for _, q := range []string{
		`INSERT INTO site_groups (id, name, parent_id, created_at) VALUES (1, 'Clients', NULL, '2026-10-30T10:00:00Z')`,
		`INSERT INTO sites (name, path, php_minor, domain, https, created_at, enabled, group_id) VALUES ('shop', '/Users/test/Sites/My Shop', '8.4', 'shop.localhost', 1, '2026-10-30T10:00:00Z', 1, 1)`,
		`INSERT INTO sites (name, path, php_minor, domain, https, created_at, enabled) VALUES ('legacy', '/Users/test/Sites/legacy', '8.2', 'legacy.localhost', 1, '2026-10-30T10:00:00Z', 0)`,
		`INSERT INTO site_aliases (site, domain) VALUES ('shop', 'www.shop.localhost')`,
		`INSERT INTO settings (key, value) VALUES ('https_port', '9443')`,
	} {
		if _, err := old.db.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = old.Close()

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer s.Close()
	shop, err := s.Site(ctx, "shop")
	if err != nil {
		t.Fatal(err)
	}
	if shop.Path != "/Users/test/Sites/My Shop" || shop.PHPMinor != "8.4" || !shop.Enabled || shop.GroupID != 1 ||
		shop.Title != "" || shop.Position != 0 || len(shop.Aliases) != 1 || shop.Aliases[0] != "www.shop.localhost" {
		t.Fatalf("shop after migration = %+v", shop)
	}
	legacy, err := s.Site(ctx, "legacy")
	if err != nil || legacy.Enabled {
		t.Fatalf("legacy after migration = %+v, %v", legacy, err)
	}
	if v, ok, err := s.Setting(ctx, "https_port"); err != nil || !ok || v != "9443" {
		t.Fatalf("setting = %q, %v, %v", v, ok, err)
	}
	groups, err := s.ListGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "Clients" {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
}

// An older covo (on PATH, or after going back to an older app) refuses a
// database a newer one has migrated, instead of writing to it.
func TestOpenRefusesNewerSchema(t *testing.T) {
	ctx := context.Background()
	s, path := openTest(t)
	if _, err := s.db.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (9999)`); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	if _, err := Open(ctx, path); errcode.Code(err) != errcode.Unsupported {
		t.Fatalf("Open newer schema = %v, want %s", err, errcode.Unsupported)
	}
}
