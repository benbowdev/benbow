// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"archive/zip"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestSnapshots saves a site's files and database, changes both, restores,
// and undoes the restore; .git and node_modules are never touched.
func TestSnapshots(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")
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
	h.mustBenbow("site", "add", "snap", "--php", "8.4", "--path", dir, "--database", "new")
	page := `<?php $c = json_decode(file_get_contents(__DIR__.'/db.json'), true);
$m = new mysqli('127.0.0.1', $c['user'], $c['password'], $c['database'], $c['port']);
if (isset($_GET['set'])) { $m->query('CREATE TABLE IF NOT EXISTS t (v varchar(10))'); $m->query('DELETE FROM t'); $m->query("INSERT INTO t VALUES ('".$m->real_escape_string($_GET['set'])."')"); }
$r = @$m->query('SELECT v FROM t'); echo $r ? $r->fetch_row()[0] : 'none';`
	if err := os.WriteFile(filepath.Join(dir, "db.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	creds := h.mustBenbow("db", "show", "snap")
	if err := os.WriteFile(filepath.Join(dir, "db.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	var sites []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "list"), &sites); err != nil {
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
	if err := json.Unmarshal(h.mustBenbow("snapshot", "create", "snap", "--note", "before changes"), &snap); err != nil || snap.DBName != "snap" {
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
	if err := json.Unmarshal(h.mustBenbow("snapshot", "restore", "snap", snap.ID, "--yes"), &res); err != nil || res.Before.ID == "" {
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
	if matches, _ := filepath.Glob(dir + ".benbow-restore-*"); len(matches) != 0 {
		t.Fatalf("left behind: %v", matches)
	}

	// Exported as a zip, the snapshot comes back with import archive as a
	// new site with the same files and database.
	zipPath := filepath.Join(t.TempDir(), "snap.zip")
	var exp struct {
		Path     string `json:"path"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "export", "snap", snap.ID, "--out", zipPath), &exp); err != nil || exp.Path != zipPath || exp.Database != "snap.sql" {
		t.Fatalf("export = %+v, %v", exp, err)
	}
	if zr, err := zip.OpenReader(zipPath); err == nil {
		for _, f := range zr.File {
			if strings.HasPrefix(f.Name, "snap/.git/") || strings.HasPrefix(f.Name, "snap/node_modules/") {
				t.Errorf("%s in the export", f.Name)
			}
		}
		_ = zr.Close()
	} else {
		t.Fatal(err)
	}
	h.mustBenbow("import", "archive", zipPath, "--name", "snapcopy", "--folder", filepath.Join(t.TempDir(), "snapcopy"))
	var rows struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "snapcopy", "SELECT v FROM t"), &rows); err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "one" {
		t.Fatalf("imported export: %+v, %v", rows, err)
	}
	h.mustBenbow("site", "remove", "snapcopy", "--drop-database", "--yes")

	// A restore cut short between files and database comes back as a
	// consistent pair: the state before the restore, files and database.
	h.mustBenbow("snapshot", "restore", "snap", res.Before.ID, "--yes")
	h.crashRestore("files", snap.ID)
	if b, _ := os.ReadFile(filepath.Join(dir, "index.php")); string(b) != "<?php echo 'v1';" {
		t.Fatalf("files not restored before the crash: %q", b)
	}
	h.daemon("start") // recovers interrupted restores before answering
	h.daemon("stop")
	if got := h.get(url+"/", st.CA.CertPath); got != "v2" {
		t.Fatalf("index after recovering a restore cut after the files = %q", got)
	}
	if got := h.get(url+"/db.php", st.CA.CertPath); got != "two" {
		t.Fatalf("database after recovering a restore cut after the files = %q", got)
	}
	// Cut while deleting the old folder: the restore had finished, so the
	// recovery keeps it and deletes what is left of the old folder.
	h.crashRestore("done", snap.ID)
	h.daemon("start")
	h.daemon("stop")
	if got := h.get(url+"/", st.CA.CertPath); got != "v1" {
		t.Fatalf("index after recovering a finished restore = %q", got)
	}
	if got := h.get(url+"/db.php", st.CA.CertPath); got != "one" {
		t.Fatalf("database after recovering a finished restore = %q", got)
	}
	if matches, _ := filepath.Glob(dir + ".benbow-restore-*"); len(matches) != 0 {
		t.Fatalf("left behind after recovery: %v", matches)
	}
	var withVersion []struct {
		DBVersion string `json:"database_version"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "list", "snap"), &withVersion); err != nil || withVersion[0].DBVersion == "" {
		t.Fatalf("snapshot database version = %+v, %v", withVersion, err)
	}
	// Undo the last restore again with the pre-restore snapshot of the run
	// above, so the checks below start from the changed state.
	h.mustBenbow("snapshot", "restore", "snap", res.Before.ID, "--yes")
	if matches, _ := filepath.Glob(filepath.Join(h.home(), "data", "restores", "*.json")); len(matches) != 0 {
		t.Fatalf("restore journals left: %v", matches)
	}

	// Undo the restore with the snapshot it took.
	h.mustBenbow("snapshot", "restore", "snap", res.Before.ID, "--yes")
	if got := h.get(url+"/", st.CA.CertPath); got != "v2" {
		t.Fatalf("index after undo = %q", got)
	}
	if got := h.get(url+"/db.php", st.CA.CertPath); got != "two" {
		t.Fatalf("database after undo = %q", got)
	}

	var list []struct {
		Kind string `json:"kind"`
	}
	// The snapshot, and one pre-restore snapshot per restore (six, the two
	// cut short included).
	if err := json.Unmarshal(h.mustBenbow("snapshot", "list", "snap"), &list); err != nil || len(list) != 7 {
		t.Fatalf("snapshots = %+v, %v", list, err)
	}
	h.mustBenbow("snapshot", "keep", "2")
	h.mustBenbow("snapshot", "create", "snap")
	if err := json.Unmarshal(h.mustBenbow("snapshot", "list", "snap"), &list); err != nil || len(list) != 2 {
		t.Fatalf("snapshots after keep 2 = %+v, %v", list, err)
	}

	// The PHP version and php.ini values come back only when asked.
	var withPHP struct {
		ID  string `json:"id"`
		PHP string `json:"php"`
	}
	h.mustBenbow("site", "php", "snap", "memory_limit=300M")
	if err := json.Unmarshal(h.mustBenbow("snapshot", "create", "snap"), &withPHP); err != nil || withPHP.PHP != "8.4" {
		t.Fatalf("snapshot PHP = %+v, %v", withPHP, err)
	}
	h.mustBenbow("site", "set", "snap", "--php", "8.2")
	h.mustBenbow("site", "php", "snap", "memory_limit=")
	var r struct {
		PHPDiffers  bool `json:"php_differs"`
		PHPRestored bool `json:"php_restored"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "restore", "snap", withPHP.ID, "--yes"), &r); err != nil || !r.PHPDiffers || r.PHPRestored {
		t.Fatalf("restore without --php = %+v, %v", r, err)
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "restore", "snap", withPHP.ID, "--yes", "--php"), &r); err != nil || !r.PHPRestored {
		t.Fatalf("restore --php = %+v, %v", r, err)
	}
	var site []struct {
		Name        string            `json:"name"`
		PHP         string            `json:"php"`
		PHPSettings map[string]string `json:"php_settings"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "list"), &site); err != nil || site[0].PHP != "8.4" || site[0].PHPSettings["memory_limit"] != "300M" {
		t.Fatalf("site after restore --php = %+v, %v", site, err)
	}

	var usage struct {
		Snapshots []struct {
			Private  int64 `json:"private"`
			Apparent int64 `json:"apparent"`
		} `json:"snapshots"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "usage"), &usage); err != nil || len(usage.Snapshots) == 0 || usage.Snapshots[0].Apparent == 0 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	var pruned struct {
		Removed []struct{} `json:"removed"`
		DryRun  bool       `json:"dry_run"`
	}
	if err := json.Unmarshal(h.mustBenbow("snapshot", "prune", "--older-than", "1h", "--dry-run"), &pruned); err != nil || !pruned.DryRun || len(pruned.Removed) != 0 {
		t.Fatalf("prune dry run = %+v, %v", pruned, err)
	}
}

// crashRestore runs a restore that exits at stage (benbowtest builds only).
func (h *harness) crashRestore(stage, id string) {
	h.t.Helper()
	saved := h.env
	h.env = append(slices.Clone(saved), "BENBOW_TEST_RESTORE_CRASH="+stage)
	defer func() { h.env = saved }()
	if out, code := h.raw("snapshot", "restore", "snap", id, "--yes"); code != 86 {
		h.t.Fatalf("restore with a crash at %s exited %d: %s", stage, code, out)
	}
}
