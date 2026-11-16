// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// TestImportArchive packs a WordPress site like a Local export (folder in
// app/public, dump in app/sql), deletes it, and creates it again from the
// zip under another name: database loaded, wp-config.php pointed at it,
// address replaced, dump kept out of the web folder.
func TestImportArchive(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")
	work := t.TempDir()
	src := filepath.Join(work, "shop")
	h.mustBenbow("wordpress", "shop", "--path", src)
	dump := filepath.Join(work, "local.sql")
	if out, code := h.raw("wp", "shop", "db", "export", dump); code != 0 {
		t.Fatalf("db export = %d: %s", code, out)
	}

	archive := filepath.Join(work, "Shop Export.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, file string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		r, err := os.Open(file)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		if _, err := io.Copy(w, r); err != nil {
			t.Fatal(err)
		}
	}
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		add("app/public/"+filepath.ToSlash(rel), p)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	add("app/sql/local.sql", dump)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	dupZip := packDuplicator(t, h, src, work)
	h.mustBenbow("site", "remove", "shop", "--drop-database", "--yes")

	dest := filepath.Join(work, "restored")
	var plan struct {
		Name   string `json:"name"`
		Root   string `json:"root"`
		SQL    string `json:"sql"`
		DryRun bool   `json:"dry_run"`
	}
	if err := json.Unmarshal(h.mustBenbow("import", "archive", archive, "--folder", dest, "--dry-run"), &plan); err != nil {
		t.Fatal(err)
	}
	if plan.Name != "shop-export" || plan.Root != "app/public" || plan.SQL != "app/sql/local.sql" || !plan.DryRun {
		t.Fatalf("plan = %+v", plan)
	}
	if _, err := os.Stat(dest); err == nil {
		t.Fatal("a dry run created the folder")
	}

	var res struct {
		Site struct {
			URL string `json:"url"`
		} `json:"site"`
		Database  string   `json:"database"`
		WordPress string   `json:"wordpress"`
		URLFrom   string   `json:"url_from"`
		Warnings  []string `json:"warnings"`
	}
	if err := json.Unmarshal(h.mustBenbow("import", "archive", archive, "--folder", dest, "--name", "restored"), &res); err != nil {
		t.Fatal(err)
	}
	if res.Database == "" || res.WordPress == "" || !strings.Contains(res.URLFrom, "shop.localhost") || !strings.Contains(res.Site.URL, "restored.localhost") {
		t.Fatalf("import = %+v", res)
	}
	cfg, _ := os.ReadFile(filepath.Join(dest, "wp-config.php"))
	if !strings.Contains(string(cfg), "'"+res.Database+"'") {
		t.Errorf("wp-config.php not pointed at %s", res.Database)
	}
	if matches, _ := filepath.Glob(filepath.Join(dest, "*.sql")); len(matches) > 0 {
		t.Errorf("dump inside the site folder: %v", matches)
	}
	var q struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "restored", "SELECT option_value FROM wp_options WHERE option_name = 'home'"), &q); err != nil || len(q.Rows) != 1 || q.Rows[0][0] != res.Site.URL {
		t.Fatalf("home = %+v, %v (want %s)", q, err, res.Site.URL)
	}
	st := h.status()
	if body := h.get(res.Site.URL+"/", st.CA.CertPath); !strings.Contains(body, "restored.localhost") {
		t.Errorf("page does not link to its own address:\n%.400s", body)
	}

	// A folder that is not empty is refused, and nothing is created.
	if env := h.benbow("import", "archive", archive, "--folder", dest, "--name", "again"); env.OK || env.Error.Code != "conflict" {
		t.Errorf("import into a full folder = %+v", env)
	}

	// A Duplicator 5 backup: one dump per table, wp-config.php kept apart
	// without the database settings, installer files next to the site.
	dupDest := filepath.Join(work, "fromdup")
	res.Warnings = nil
	if err := json.Unmarshal(h.mustBenbow("import", "archive", dupZip, "--folder", dupDest), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Site.URL, "dupshop.localhost") || !strings.Contains(res.URLFrom, "shop.localhost") {
		t.Fatalf("Duplicator import = %+v", res)
	}
	cfg, _ = os.ReadFile(filepath.Join(dupDest, "wp-config.php"))
	if !strings.Contains(string(cfg), "'"+res.Database+"'") {
		t.Errorf("Duplicator's wp-config.php not pointed at %s", res.Database)
	}
	for _, name := range []string{"dup-installer", "installer-backup.php"} {
		if _, err := os.Stat(filepath.Join(dupDest, name)); err == nil {
			t.Errorf("%s left in the site's folder", name)
		}
	}
	var n struct {
		Rows [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "dupshop", "SELECT COUNT(*) FROM wp_posts"), &n); err != nil || len(n.Rows) != 1 || n.Rows[0][0] == "0" {
		t.Fatalf("posts = %+v, %v", n, err)
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "dupshop", "SELECT option_value FROM wp_options WHERE option_name = 'home'"), &q); err != nil || len(q.Rows) != 1 || q.Rows[0][0] != res.Site.URL {
		t.Fatalf("Duplicator home = %+v, %v (want %s)", q, err, res.Site.URL)
	}
	if body := h.get(res.Site.URL+"/", st.CA.CertPath); !strings.Contains(body, "dupshop.localhost") {
		t.Errorf("Duplicator site page:\n%.400s", body)
	}
}

// packDuplicator packs a WordPress site the way Duplicator 5 does, as
// read from its source: site files at the top without wp-config.php,
// dup-installer/dup_descriptors_<hash>/db_dumps/ with schema.sql.gz and one
// gzip dump per table, a copy of wp-config.php without the database
// settings in orig_files/source_site_wpconfig, and installer-backup.php.
func packDuplicator(t *testing.T, h *harness, src, work string) string {
	t.Helper()
	desc := "dup-installer/dup_descriptors_6f1c2a9b0d3e4f5a1234_20261116120000/"
	files := map[string][]byte{}
	gz := func(b []byte) []byte {
		var buf bytes.Buffer
		w := gzip.NewWriter(&buf)
		_, _ = w.Write(b)
		_ = w.Close()
		return buf.Bytes()
	}
	export := func(args ...string) []byte {
		out := filepath.Join(work, "part.sql")
		if o, code := h.raw(append([]string{"wp", "shop", "db", "export", out}, args...)...); code != 0 {
			t.Fatalf("db export %v = %d: %s", args, code, o)
		}
		b, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	files[desc+"db_dumps/schema.sql.gz"] = gz(export("--no-data=true"))
	tables, code := h.raw("wp", "shop", "db", "tables", "--all-tables-with-prefix")
	if code != 0 {
		t.Fatalf("db tables: %s", tables)
	}
	for _, table := range strings.Fields(tables) {
		files[desc+"db_dumps/"+table+"-h0123456789abcdef.sql.gz"] = gz(export("--tables="+table, "--no-create-info=true"))
	}
	cfg, err := os.ReadFile(filepath.Join(src, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}
	blank := regexp.MustCompile(`define\(\s*'(DB_NAME|DB_USER|DB_PASSWORD|DB_HOST)',\s*'[^']*'\s*\)`)
	files[desc+"orig_files/source_site_wpconfig"] = blank.ReplaceAll(cfg, []byte("define( '$1', '' )"))
	files["installer-backup.php"] = []byte("<?php // Duplicator installer")
	files["dup-installer/main.installer.php"] = []byte("<?php // Duplicator installer")
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == "wp-config.php" {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(work, "dupshop_6f1c2a9b0d3e4f5a1234_20261116120000_archive.zip")
	f, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	for name, b := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(b); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return archive
}
