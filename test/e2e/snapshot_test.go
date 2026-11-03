// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestSnapshots saves a site's files and database, changes both, restores,
// and undoes the restore; .git and node_modules are never touched.
func TestSnapshots(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")
	dir := filepath.Join(t.TempDir(), "snap")
	for f, body := range map[string]string{
		"index.php":           "<?php echo 'v1';",
		".git/HEAD":           "ref: refs/heads/main\n",
		"node_modules/x/a.js": "1",
		"theme/style.css":     "v1",
	} {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.mustCovo("site", "add", "snap", "--php", "8.4", "--path", dir, "--database", "new")
	page := `<?php $c = json_decode(file_get_contents(__DIR__.'/db.json'), true);
$m = new mysqli('127.0.0.1', $c['user'], $c['password'], $c['database'], $c['port']);
if (isset($_GET['set'])) { $m->query('CREATE TABLE IF NOT EXISTS t (v varchar(10))'); $m->query('DELETE FROM t'); $m->query("INSERT INTO t VALUES ('".$m->real_escape_string($_GET['set'])."')"); }
$r = @$m->query('SELECT v FROM t'); echo $r ? $r->fetch_row()[0] : 'none';`
	if err := os.WriteFile(filepath.Join(dir, "db.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	creds := h.mustCovo("db", "show", "snap")
	if err := os.WriteFile(filepath.Join(dir, "db.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	var sites []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil {
		t.Fatal(err)
	}
	st := h.status()
	url := sites[0].URL
	if got := h.get(url+"/db.php?set=one", st.CA.CertPath); got != "one" {
		t.Fatalf("set one = %q", got)
	}

	var snap struct {
		ID     string `json:"id"`
		DBName string `json:"database"`
	}
	if err := json.Unmarshal(h.mustCovo("snapshot", "create", "snap", "--note", "before changes"), &snap); err != nil || snap.DBName != "snap" {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
	// Change files, the database, and git (which must survive the restore).
	_ = os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo 'v2';"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/feature\n"), 0o644)
	if got := h.get(url+"/db.php?set=two", st.CA.CertPath); got != "two" {
		t.Fatalf("set two = %q", got)
	}

	var res struct {
		Before struct {
			ID string `json:"id"`
		} `json:"before"`
	}
	if err := json.Unmarshal(h.mustCovo("snapshot", "restore", "snap", snap.ID, "--yes"), &res); err != nil || res.Before.ID == "" {
		t.Fatalf("restore = %+v, %v", res, err)
	}
	if got := h.get(url+"/", st.CA.CertPath); got != "v1" {
		t.Fatalf("index after restore = %q", got)
	}
	if got := h.get(url+"/db.php", st.CA.CertPath); got != "one" {
		t.Fatalf("database after restore = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "new.txt")); !os.IsNotExist(err) {
		t.Fatal("a file created after the snapshot survived the restore")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".git", "HEAD")); string(b) != "ref: refs/heads/feature\n" {
		t.Fatalf(".git was restored: %q", b)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules", "x", "a.js")); err != nil {
		t.Fatal("node_modules lost:", err)
	}
	if matches, _ := filepath.Glob(dir + ".covo-restore-*"); len(matches) != 0 {
		t.Fatalf("left behind: %v", matches)
	}

	// Undo the restore with the snapshot it took.
	h.mustCovo("snapshot", "restore", "snap", res.Before.ID, "--yes")
	if got := h.get(url+"/", st.CA.CertPath); got != "v2" {
		t.Fatalf("index after undo = %q", got)
	}
	if got := h.get(url+"/db.php", st.CA.CertPath); got != "two" {
		t.Fatalf("database after undo = %q", got)
	}

	var list []struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(h.mustCovo("snapshot", "list", "snap"), &list); err != nil || len(list) != 3 {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	h.mustCovo("snapshot", "keep", "2")
	h.mustCovo("snapshot", "create", "snap")
	if err := json.Unmarshal(h.mustCovo("snapshot", "list", "snap"), &list); err != nil || len(list) != 2 {
		t.Fatalf("snapshots after keep 2 = %+v, %v", list, err)
	}
}
