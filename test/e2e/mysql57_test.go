// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestMySQL57 runs the optional MySQL 5.7 server next to 8.4: a site's
// database on it, PHP connecting, read-only queries, snapshot and clone.
// The 5.7 build is not published yet: BENBOW_E2E_MYSQL57 names a local
// archive made by benbow-binaries' mysql57/build.sh, served with a signed
// test manifest.
func TestMySQL57(t *testing.T) {
	archive := os.Getenv("BENBOW_E2E_MYSQL57")
	if archive == "" || runtime.GOARCH != "arm64" {
		t.Skip("set BENBOW_E2E_MYSQL57 to a mysql-5.7.x-macos-arm64.tar.gz (Apple Silicon)")
	}
	body, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	version := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(archive), "mysql-"), "-macos-arm64.tar.gz")

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := &manifestServer{}
	mux := http.NewServeMux()
	mux.Handle("/", srv)
	mux.HandleFunc("/mysql57.tar.gz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(body) })
	ts := httptest.NewServer(mux)
	defer ts.Close()
	var embedded struct {
		Serial int64 `json:"serial"`
	}
	raw, err := os.ReadFile("../../internal/components/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &embedded)
	manifest, _ := json.Marshal(map[string]any{
		"serial": embedded.Serial + 1, "expires": time.Now().Add(14 * 24 * time.Hour).UTC().Format(time.RFC3339),
		"artifacts": []map[string]any{{
			"component": "mysql57", "version": version, "arch": "arm64", "url": ts.URL + "/mysql57.tar.gz",
			"sha256": hex.EncodeToString(sum[:]), "files": []string{"bin/", "lib/", "share/"},
		}},
	})
	srv.set(manifest, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest))))

	h := newHarness(t)
	h.env = append(h.env, "BENBOW_MANIFEST_URL="+ts.URL+"/manifest.json",
		"BENBOW_MANIFEST_RELEASE_URL="+ts.URL+"/release/manifest.json",
		"BENBOW_MANIFEST_TEST_KEY="+base64.StdEncoding.EncodeToString(pub))
	if env := h.benbow("mysql57", "enable"); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("enable before the build is in the manifest = %+v", env)
	}
	h.mustBenbow("update", "check")

	var st struct {
		Engine  string `json:"engine"`
		Enabled bool   `json:"enabled"`
		Version string `json:"version"`
		Port    int    `json:"port"`
	}
	if err := json.Unmarshal(h.mustBenbow("mysql57", "enable"), &st); err != nil || !st.Enabled || st.Version != version || st.Engine != "mysql57" {
		t.Fatalf("mysql57 enable = %+v, %v", st, err)
	}
	h.mustBenbow("mysql", "enable")
	var main struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal(h.mustBenbow("mysql"), &main)
	if main.Port == st.Port {
		t.Fatalf("both servers on port %d", st.Port)
	}
	h.mustBenbow("start")

	dir := newSite(t)
	h.mustBenbow("site", "add", "old", "--php", "8.4", "--path", dir)
	var db struct {
		Engine string `json:"engine"`
		Port   int    `json:"port"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "create", "old", "--engine", "mysql57"), &db); err != nil || db.Engine != "mysql57" || db.Port != st.Port {
		t.Fatalf("db create = %+v, %v", db, err)
	}
	creds := h.mustBenbow("db", "show", "old")
	if err := os.WriteFile(filepath.Join(dir, "db.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	page := `<?php $c = json_decode(file_get_contents(__DIR__.'/db.json'), true);
$m = new mysqli('127.0.0.1', $c['user'], $c['password'], $c['database'], $c['port']);
if (isset($_GET['set'])) { $m->query('CREATE TABLE IF NOT EXISTS t (v varchar(10))'); $m->query('DELETE FROM t'); $m->query("INSERT INTO t VALUES ('".$m->real_escape_string($_GET['set'])."')"); }
$r = @$m->query('SELECT v FROM t'); echo $m->server_info, ' ', $r ? $r->fetch_row()[0] : 'none';`
	if err := os.WriteFile(filepath.Join(dir, "db.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	var sites []struct {
		URL string `json:"url"`
	}
	_ = json.Unmarshal(h.mustBenbow("site", "list"), &sites)
	status := h.status()
	if got := h.get(sites[0].URL+"/db.php?set=one", status.CA.CertPath); got != version+" one" {
		t.Fatalf("PHP on MySQL 5.7 = %q", got)
	}

	var q struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "old", "SELECT v FROM t"), &q); err != nil || len(q.Rows) != 1 || q.Rows[0][0] != "one" {
		t.Fatalf("read-only query = %+v, %v", q, err)
	}
	if env := h.benbow("db", "query", "old", "DELETE FROM t"); env.OK {
		t.Error("a write went through the read-only query")
	}

	var snap struct {
		ID     string `json:"id"`
		Engine string `json:"database_engine"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "create", "old"), &snap); err != nil || snap.Engine != "mysql57" {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	_ = h.get(sites[0].URL+"/db.php?set=two", status.CA.CertPath)
	h.mustBenbow("snapshot", "restore", "old", snap.ID, "--yes")
	if got := h.get(sites[0].URL+"/db.php", status.CA.CertPath); got != version+" one" {
		t.Fatalf("after restore = %q", got)
	}

	var cl struct {
		Database string `json:"database"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "clone", "old", "old-copy"), &cl); err != nil || cl.Database == "" {
		t.Fatalf("clone = %+v, %v", cl, err)
	}
	if err := json.Unmarshal(h.mustBenbow("db", "show", "old-copy"), &db); err != nil || db.Engine != "mysql57" {
		t.Fatalf("clone's database = %+v, %v", db, err)
	}

	h.mustBenbow("mysql57", "disable")
	if env := h.benbow("db", "query", "old", "SELECT 1"); env.OK {
		t.Error("query worked with MySQL 5.7 off")
	}
}
