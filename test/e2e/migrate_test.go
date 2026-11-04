// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/wpconfig"
)

// TestImportMigrate imports a WordPress site from another tool (here a
// second, separate Covo stands in for MAMP: its own MySQL, its own
// WordPress) through the Valet importer, migrates it (database copied from
// the other server, wp-config.php pointed at Covo, address replaced), then
// undoes the migration.
func TestImportMigrate(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	src := newHarness(t)
	src.mustCovo("mysql", "enable")
	src.mustCovo("start")
	wp := filepath.Join(t.TempDir(), "legacy")
	src.mustCovo("wordpress", "legacy", "--path", wp)
	var srcMySQL struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(src.mustCovo("mysql", "password"), &srcMySQL); err != nil {
		t.Fatal(err)
	}
	var srcHome struct {
		Output string `json:"output"`
	}
	_ = srcHome
	// As in MAMP: the site connects as root over TCP to "the other" MySQL.
	cfgPath := filepath.Join(wp, "wp-config.php")
	orig, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	asMAMP, err := wpconfig.Set(string(orig), map[string]string{
		"DB_USER": "root", "DB_PASSWORD": srcMySQL.Password, "DB_HOST": "127.0.0.1:" + strconv.Itoa(srcMySQL.Port),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(asMAMP), 0o644); err != nil {
		t.Fatal(err)
	}
	// As some MAMP sites: an address without a domain suffix, which is the
	// start of the new one (https://legacy.localhost:…): replaced once only.
	for _, opt := range []string{"home", "siteurl"} {
		if out, code := src.raw("wp", "legacy", "option", "update", opt, "http://legacy"); code != 0 {
			t.Fatalf("source %s: %s", opt, out)
		}
	}
	oldHome, code := src.raw("wp", "legacy", "option", "get", "home")
	if code != 0 {
		t.Fatalf("source home: %s", oldHome)
	}
	oldHome = strings.TrimSpace(oldHome)

	// A Valet configuration that links the same folder.
	h := newHarness(t)
	home := t.TempDir()
	valet := filepath.Join(home, ".config", "valet")
	if err := os.MkdirAll(filepath.Join(valet, "Sites"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valet, "config.json"), []byte(`{"tld":"test","paths":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wp, filepath.Join(valet, "Sites", "legacy")); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "COVO_IMPORT_HOME="+home)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")

	type plan struct {
		Create   int `json:"create"`
		Migrated int `json:"migrated"`
		Items    []struct {
			Name      string `json:"name"`
			Action    string `json:"action"`
			Migration *struct {
				Database string   `json:"database"`
				Steps    []string `json:"steps"`
				Problem  string   `json:"problem"`
				Error    string   `json:"error"`
				Done     bool     `json:"done"`
				URLFrom  string   `json:"url_from"`
			} `json:"migration"`
		} `json:"items"`
	}
	var dry plan
	if err := json.Unmarshal(h.mustCovo("import", "valet", "--migrate", "--localhost", "--dry-run"), &dry); err != nil {
		t.Fatal(err)
	}
	if dry.Create != 1 || dry.Items[0].Migration == nil || dry.Items[0].Migration.Database != "legacy" || len(dry.Items[0].Migration.Steps) < 3 {
		t.Fatalf("dry run = %s", h.mustCovo("import", "valet", "--migrate", "--localhost", "--dry-run"))
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != asMAMP {
		t.Fatal("the dry run changed wp-config.php")
	}

	var done plan
	data := h.mustCovo("import", "valet", "--migrate", "--localhost")
	if err := json.Unmarshal(data, &done); err != nil {
		t.Fatal(err)
	}
	m := done.Items[0].Migration
	if done.Migrated != 1 || m == nil || !m.Done || m.Error != "" || m.URLFrom != oldHome {
		t.Fatalf("migration = %s", data)
	}
	var tgt struct {
		Port int `json:"port"`
	}
	if err := json.Unmarshal(h.mustCovo("mysql"), &tgt); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(cfgPath)
	cfg := wpconfig.Read(string(b))
	if cfg.Values["DB_HOST"] != "127.0.0.1:"+strconv.Itoa(tgt.Port) || cfg.Values["DB_USER"] != "legacy" || cfg.Values["DB_NAME"] != "legacy" {
		t.Fatalf("wp-config after migration: %v", cfg.Values)
	}
	var sites []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil || len(sites) != 1 {
		t.Fatal(err)
	}
	newHome, code := h.raw("wp", "legacy", "option", "get", "home")
	if code != 0 || strings.TrimSpace(newHome) != sites[0].URL {
		t.Fatalf("home after migration = %q (want %s)", newHome, sites[0].URL)
	}
	st := h.status()
	if body := h.get(sites[0].URL+"/", st.CA.CertPath); !strings.Contains(body, sites[0].URL) {
		t.Fatalf("migrated site page: %.300q", body)
	}
	backups, _ := filepath.Glob(filepath.Join(st.Home, "data", "backups", "legacy", "wp-config.php.*"))
	if len(backups) != 1 {
		t.Fatalf("backups = %v", backups)
	}

	// The source database was only read: still there, still the old address.
	var srcDBs []string
	if err := json.Unmarshal(src.mustCovo("db", "list"), &srcDBs); err != nil || !strings.Contains(strings.Join(srcDBs, ","), "legacy") {
		t.Fatalf("source databases = %v, %v", srcDBs, err)
	}

	// Undo: wp-config.php back byte for byte, the copied database gone.
	h.mustCovo("import", "undo", "legacy")
	if b, _ := os.ReadFile(cfgPath); string(b) != asMAMP {
		t.Fatal("undo did not restore wp-config.php")
	}
	var dbs []string
	if err := json.Unmarshal(h.mustCovo("db", "list"), &dbs); err != nil || len(dbs) != 0 {
		t.Fatalf("databases after undo = %v, %v", dbs, err)
	}
	if env := h.covo("import", "undo", "legacy"); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("second undo = %+v", env)
	}
}
