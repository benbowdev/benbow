// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pgPage connects to the site database with PDO pgsql (Swoole's driver in
// the static PHP builds) and to Redis with phpredis.
const pgPage = `<?php
$c = json_decode(file_get_contents(__DIR__ . '/db.json'), true);
// Through the socket: Swoole's PDO pgsql hangs over TCP (docs/postgres-redis.md).
$pdo = new PDO("pgsql:host={$c['socket']};port={$c['port']};dbname={$c['database']}", $c['user'], $c['password'],
	[PDO::ATTR_ERRMODE => PDO::ERRMODE_EXCEPTION]);
$pdo->exec('CREATE TABLE IF NOT EXISTS t (v int)');
$pdo->exec('INSERT INTO t VALUES (1)');
$n = $pdo->query('SELECT count(*) FROM t')->fetchColumn();
$r = new Redis();
$r->connect('127.0.0.1', (int) $c['redis_port']);
$r->set('covo', 'ok');
echo $n, '|', $r->get('covo');
`

// TestPostgresRedis runs PostgreSQL and Redis, gives a site its own
// PostgreSQL database, reaches both from PHP and runs Laravel on them.
func TestPostgresRedis(t *testing.T) {
	h := newHarness(t)
	var pg, rd struct {
		Enabled bool `json:"enabled"`
		Port    int  `json:"port"`
		Running bool `json:"running"`
	}
	if err := json.Unmarshal(h.mustCovo("postgres", "enable"), &pg); err != nil || !pg.Enabled {
		t.Fatalf("postgres enable = %+v, %v", pg, err)
	}
	if err := json.Unmarshal(h.mustCovo("redis", "enable"), &rd); err != nil || !rd.Enabled {
		t.Fatalf("redis enable = %+v, %v", rd, err)
	}
	h.mustCovo("start")

	dir := newSite(t)
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(pgPage), 0o644); err != nil {
		t.Fatal(err)
	}
	var site struct {
		URL      string `json:"url"`
		Database string `json:"database"`
		Engine   string `json:"database_engine"`
	}
	data := h.mustCovo("site", "add", "pgshop", "--php", "8.4", "--path", dir, "--database", "new", "--engine", "pgsql")
	if err := json.Unmarshal(data, &site); err != nil || site.Database != "pgshop" || site.Engine != "pgsql" {
		t.Fatalf("site add = %s, %v", data, err)
	}
	var db map[string]any
	if err := json.Unmarshal(h.mustCovo("db", "show", "pgshop"), &db); err != nil || db["engine"] != "pgsql" || db["user"] != "pgshop" {
		t.Fatalf("db show = %v, %v", db, err)
	}
	if err := json.Unmarshal(h.mustCovo("redis"), &rd); err != nil {
		t.Fatal(err)
	}
	db["redis_port"] = rd.Port
	creds, _ := json.Marshal(db)
	if err := os.WriteFile(filepath.Join(dir, "db.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	st := h.status()
	if body := h.get(site.URL, st.CA.CertPath); body != "1|ok" {
		t.Fatalf("page = %q", body)
	}

	// A snapshot restores the PostgreSQL database too (each request adds a row).
	var snap struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(h.mustCovo("snapshot", "create", "pgshop"), &snap); err != nil {
		t.Fatal(err)
	}
	if body := h.get(site.URL, st.CA.CertPath); body != "2|ok" {
		t.Fatalf("second request = %q", body)
	}
	h.mustCovo("snapshot", "restore", "pgshop", snap.ID, "--yes")
	if body := h.get(site.URL, st.CA.CertPath); body != "2|ok" {
		t.Fatalf("after restore = %q (want the snapshot's 1 row plus this one)", body)
	}

	var dbs []string
	if err := json.Unmarshal(h.mustCovo("db", "list", "--engine", "pgsql"), &dbs); err != nil || strings.Join(dbs, ",") != "pgshop" {
		t.Fatalf("db list = %v, %v", dbs, err)
	}
	if env := h.covo("db", "create", "pgshop", "--engine", "pgsql"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("second create = %+v", env)
	}
	if !h.keychainAccountIn(".postgres", "pgshop") {
		t.Fatal("site password not in the keychain")
	}
	h.mustCovo("db", "unlink", "pgshop", "--drop", "--yes")
	if err := json.Unmarshal(h.mustCovo("db", "list", "--engine", "pgsql"), &dbs); err != nil || len(dbs) != 0 {
		t.Fatalf("db list after drop = %v, %v", dbs, err)
	}

	// Laravel on PostgreSQL and Redis.
	app := filepath.Join(t.TempDir(), "api")
	var lv struct {
		URL      string `json:"url"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(h.mustCovo("laravel", "api", "--path", app, "--engine", "pgsql"), &lv); err != nil || lv.Database != "api" {
		t.Fatalf("laravel = %+v, %v", lv, err)
	}
	env, err := os.ReadFile(filepath.Join(app, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"DB_CONNECTION=pgsql", "DB_PORT=" + strconv.Itoa(pg.Port), "CACHE_STORE=redis", "REDIS_PORT=" + strconv.Itoa(rd.Port)} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf(".env lacks %q", want)
		}
	}
	if !strings.Contains(string(env), "DB_HOST=/") && !strings.Contains(string(env), `DB_HOST="/`) {
		t.Errorf("home %s; .env does not point at the socket folder:\n%s", st.Home, env)
	}
	if out, code := h.raw("artisan", "api", "migrate:status"); code != 0 || !strings.Contains(out, "Ran") {
		t.Fatalf("migrate:status on PostgreSQL (%d):\n%s", code, out)
	}
	if out, code := h.raw("artisan", "api", "tinker", "--execute=cache()->put('k','v'); echo cache()->get('k');"); code != 0 || !strings.Contains(out, "v") {
		t.Fatalf("Redis cache through Laravel (%d):\n%s", code, out)
	}
	if body := h.get(lv.URL, st.CA.CertPath); !strings.Contains(body, "<title>api</title>") {
		t.Fatalf("Laravel page: %.200q", body)
	}
}
