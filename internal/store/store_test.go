// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
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
