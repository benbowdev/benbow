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

	"github.com/benbowdev/benbow/internal/wpconfig"
)

// TestImportMigrate imports a WordPress site from another tool (here a
// second, separate Benbow stands in for MAMP: its own MySQL, its own
// WordPress) through the Valet importer, migrates it (database copied from
// the other server, wp-config.php pointed at Benbow, address replaced), then
// undoes the migration.
func TestImportMigrate(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	src := newHarness(t)
	src.mustBenbow("mysql", "enable")
	src.mustBenbow("start")
	wp := filepath.Join(t.TempDir(), "legacy")
	src.mustBenbow("wordpress", "legacy", "--path", wp)
	var srcMySQL struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(src.mustBenbow("mysql", "password"), &srcMySQL); err != nil {
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
	// The address also written in wp-config.php (after the options: with
	// WP_HOME set, wp option update home writes nothing). It wins over the
	// database, so the migration has to change it too.
	asMAMP = strings.Replace(asMAMP, "<?php\n", "<?php\ndefine( 'WP_HOME', 'http://legacy' );\ndefine( 'WP_SITEURL', 'http://legacy' );\n", 1)
	if err := os.WriteFile(cfgPath, []byte(asMAMP), 0o644); err != nil {
		t.Fatal(err)
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
	h.env = append(h.env, "BENBOW_IMPORT_HOME="+home)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")

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
	if err := json.Unmarshal(h.mustBenbow("import", "valet", "--migrate", "--localhost", "--dry-run"), &dry); err != nil {
		t.Fatal(err)
	}
	if dry.Create != 1 || dry.Items[0].Migration == nil || dry.Items[0].Migration.Database != "legacy" || len(dry.Items[0].Migration.Steps) < 3 {
		t.Fatalf("dry run = %s", h.mustBenbow("import", "valet", "--migrate", "--localhost", "--dry-run"))
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != asMAMP {
		t.Fatal("the dry run changed wp-config.php")
	}

	var done plan
	data := h.mustBenbow("import", "valet", "--migrate", "--localhost")
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
	if err := json.Unmarshal(h.mustBenbow("mysql"), &tgt); err != nil {
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
	if err := json.Unmarshal(h.mustBenbow("site", "list"), &sites); err != nil || len(sites) != 1 {
		t.Fatal(err)
	}
	for _, k := range wpconfig.URLKeys {
		if d := wpconfig.Lookup(string(b), k); d.Value != sites[0].URL {
			t.Errorf("%s after migration = %+v, want %s", k, d, sites[0].URL)
		}
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
	if err := json.Unmarshal(src.mustBenbow("db", "list"), &srcDBs); err != nil || !strings.Contains(strings.Join(srcDBs, ","), "legacy") {
		t.Fatalf("source databases = %v, %v", srcDBs, err)
	}

	// Undo: wp-config.php back byte for byte, the copied database gone.
	h.mustBenbow("import", "undo", "legacy")
	if b, _ := os.ReadFile(cfgPath); string(b) != asMAMP {
		t.Fatal("undo did not restore wp-config.php")
	}
	var dbs []string
	if err := json.Unmarshal(h.mustBenbow("db", "list"), &dbs); err != nil || len(dbs) != 0 {
		t.Fatalf("databases after undo = %v, %v", dbs, err)
	}
	if env := h.benbow("import", "undo", "legacy"); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("second undo = %+v", env)
	}
}

// TestImportMigrateTakenName is issue #1: an imported site whose database
// name another Benbow site already uses (Local names them all "local") gets
// a database of its own, and the other site's database is left alone.
func TestImportMigrateTakenName(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	src := newHarness(t)
	src.mustBenbow("mysql", "enable")
	src.mustBenbow("start")
	wp := filepath.Join(t.TempDir(), "shop")
	src.mustBenbow("wordpress", "shop", "--path", wp)
	var srcMySQL struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(src.mustBenbow("mysql", "password"), &srcMySQL); err != nil {
		t.Fatal(err)
	}
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

	h := newHarness(t)
	home := t.TempDir()
	valet := filepath.Join(home, ".config", "valet")
	if err := os.MkdirAll(filepath.Join(valet, "Sites"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(valet, "config.json"), []byte(`{"tld":"test","paths":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(wp, filepath.Join(valet, "Sites", "shop")); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "BENBOW_IMPORT_HOME="+home)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")
	// Another site already uses a database called "shop", with its own data.
	h.mustBenbow("wordpress", "first", "--path", filepath.Join(t.TempDir(), "first"))
	h.mustBenbow("db", "unlink", "first")
	h.mustBenbow("site", "add", "other", "--php", "8.4", "--path", filepath.Join(t.TempDir(), "other"), "--create-folder")
	h.mustBenbow("db", "create", "other", "--name", "shop")
	if _, code := h.raw("db", "query", "other", "SELECT 1"); code != 0 {
		t.Fatal("other's database is not reachable")
	}

	var res struct {
		Items []struct {
			Migration *struct {
				Database string `json:"database"`
				Target   string `json:"target"`
				Done     bool   `json:"done"`
				Error    string `json:"error"`
			} `json:"migration"`
		} `json:"items"`
	}
	data := h.mustBenbow("import", "valet", "--migrate", "--localhost")
	if err := json.Unmarshal(data, &res); err != nil || len(res.Items) != 1 || res.Items[0].Migration == nil {
		t.Fatalf("import = %s", data)
	}
	m := res.Items[0].Migration
	if !m.Done || m.Error != "" || m.Database != "shop" || m.Target != "shop_2" {
		t.Fatalf("migration = %s", data)
	}
	b, _ := os.ReadFile(cfgPath)
	if cfg := wpconfig.Read(string(b)); cfg.Values["DB_NAME"] != "shop_2" || cfg.Values["DB_USER"] != "shop_2" {
		t.Fatalf("wp-config after migration: %v", cfg.Values)
	}
	var q struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "shop", "SELECT COUNT(*) FROM wp_posts"), &q); err != nil || len(q.Rows) != 1 || q.Rows[0][0] == "0" {
		t.Fatalf("shop's own data = %+v, %v", q, err)
	}
	// The other site's database got no WordPress tables and no new address.
	if err := json.Unmarshal(h.mustBenbow("db", "query", "other", "SHOW TABLES"), &q); err != nil || len(q.Rows) != 0 {
		t.Fatalf("other's database was written to: %+v, %v", q, err)
	}
	// Linking a database another site uses needs --share; deleting it is refused.
	if env := h.benbow("db", "link", "first", "shop_2"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("link to shop's database = %+v", env)
	}
	h.mustBenbow("db", "link", "first", "shop_2", "--share")
	if env := h.benbow("db", "unlink", "first", "--drop", "--yes"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("drop a shared database = %+v", env)
	}
}
