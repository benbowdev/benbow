// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
)

// An exported snapshot is a zip `benbow import archive` reads back: the
// folder under <site>/, the dump unpacked as <site>.sql, and
// benbow-snapshot.json.
func TestExportSnapshot(t *testing.T) {
	ctx := context.Background()
	c := restoreCore(t)
	c.Layout.Tmp = t.TempDir()
	if err := c.Store.AddSite(ctx, store.Site{Name: "shop", Path: "/Users/a/shop", PHPMinor: "8.4", Domain: "shop.localhost", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.Layout.Data, "snapshots", "shop", "1")
	files := filepath.Join(dir, "files")
	writeFile(t, filepath.Join(files, "index.php"), "<?php")
	writeFile(t, filepath.Join(files, "wp-includes", "version.php"), "<?php $wp_version = '6.8';")
	writeFile(t, filepath.Join(files, "wp-content", "uploads", "a.jpg"), "jpeg")
	if err := os.Symlink("index.php", filepath.Join(files, "link.php")); err != nil {
		t.Fatal(err)
	}
	var dump bytes.Buffer
	gz := gzip.NewWriter(&dump)
	_, _ = gz.Write([]byte("CREATE TABLE wp_options (id int);\n"))
	_ = gz.Close()
	writeFile(t, filepath.Join(dir, "database.sql.gz"), dump.String())
	sn := store.Snapshot{ID: "1", Site: "shop", CreatedAt: time.Now(), Kind: "manual", Note: "before", FilesDir: files, Root: "/Users/a/shop",
		DBEngine: "mysql", DBName: "shop", DBFile: filepath.Join(dir, "database.sql.gz"), DBVersion: "8.4.13", PHPMinor: "8.4"}
	if err := writeFileList(files, fileListPath(sn)); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.AddSnapshot(ctx, sn); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(t.TempDir(), "shop.zip")
	res, err := c.ExportSnapshot(ctx, "shop", "", out)
	if err != nil {
		t.Fatal(err)
	}
	if res.Files != 4 || res.Database != "shop.sql" || res.Bytes == 0 {
		t.Errorf("export = %+v", res)
	}
	zr, err := zip.OpenReader(out)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	got := map[string]*zip.File{}
	for _, f := range zr.File {
		got[f.Name] = f
	}
	read := func(name string) string {
		f := got[name]
		if f == nil {
			t.Fatalf("%s not in the zip (%v)", name, zr.File)
		}
		r, _ := f.Open()
		defer r.Close()
		b, _ := io.ReadAll(r)
		return string(b)
	}
	if read("shop.sql") != "CREATE TABLE wp_options (id int);\n" {
		t.Error("dump not unpacked")
	}
	if read("shop/link.php") != "index.php" || got["shop/link.php"].Mode()&os.ModeSymlink == 0 {
		t.Error("symlink not kept")
	}
	if got["shop/wp-content/uploads/a.jpg"].Method != zip.Store || got["shop/index.php"].Method != zip.Deflate {
		t.Error("compression methods")
	}
	var man snapshotManifest
	if err := json.Unmarshal([]byte(read("benbow-snapshot.json")), &man); err != nil || man.Snapshot != "1" || man.DatabaseVersion != "8.4.13" || man.PHP != "8.4" {
		t.Errorf("manifest = %+v, %v", man, err)
	}

	entries, err := listArchive(out, "zip")
	if err != nil {
		t.Fatal(err)
	}
	if p, err := planArchive(entries); err != nil || p.root != "shop" || p.sql != "shop.sql" || p.framework != FrameworkWordPress {
		t.Errorf("import plan = %+v, %v", p, err)
	}
	if _, err := os.Stat(out + ".partial"); err == nil {
		t.Error("partial file left")
	}

	for _, tc := range []struct{ name, out, code string }{
		{"exists", out, errcode.Conflict},
		{"relative", "shop.zip", errcode.InvalidInput},
		{"not zip", filepath.Join(t.TempDir(), "shop.tar"), errcode.InvalidInput},
		{"no folder", filepath.Join(t.TempDir(), "gone", "shop.zip"), errcode.NotFound},
	} {
		if _, err := c.ExportSnapshot(ctx, "shop", "1", tc.out); errcode.Code(err) != tc.code {
			t.Errorf("%s: %v, want %s", tc.name, err, tc.code)
		}
	}

	// Without a path the zip goes to tmp/exports, for the app to move.
	if r, err := c.ExportSnapshot(ctx, "shop", "1", ""); err != nil || filepath.Dir(r.Path) != filepath.Join(c.Layout.Tmp, "exports") {
		t.Errorf("export to tmp = %+v, %v", r, err)
	}

	// A snapshot whose files no longer match its list is not exported.
	writeFile(t, filepath.Join(files, "index.php"), "<?php changed")
	if _, err := c.ExportSnapshot(ctx, "shop", "1", filepath.Join(t.TempDir(), "bad.zip")); err == nil {
		t.Error("damaged snapshot exported")
	}
}
