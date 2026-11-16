// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
)

func TestPlanArchive(t *testing.T) {
	e := func(names ...string) []archiveEntry {
		var out []archiveEntry
		for i, n := range names {
			out = append(out, archiveEntry{name: n, size: int64(100 * (i + 1))})
		}
		return out
	}
	tests := []struct {
		name          string
		entries       []archiveEntry
		root, sql, fw string
		parts         []string
		dup, wpConfig string
		warnings      int
		wantErr       string
	}{
		{name: "Local export", entries: e("local-site.json", "app/public/index.php", "app/public/wp-includes/version.php", "app/sql/local.sql"),
			root: "app/public", sql: "app/sql/local.sql", fw: FrameworkWordPress},
		{name: "hand-made zip with one folder", entries: e("shop/index.php", "shop/wp-includes/version.php", "shop/db.sql.gz"),
			root: "shop", sql: "shop/db.sql.gz", fw: FrameworkWordPress},
		{name: "Duplicator", entries: e("index.php", "wp-includes/version.php", "dup-installer/dup-database__x.sql", "wp-content/plugins/a/index.php"),
			root: "", sql: "dup-installer/dup-database__x.sql", fw: FrameworkWordPress, dup: "dup-installer"},
		{name: "Duplicator 5, one dump per table", entries: e("index.php", "wp-includes/version.php", "installer-backup.php",
			"dup-installer/dup_descriptors_ab12/db_dumps/wp_users-h1.sql.gz", "dup-installer/dup_descriptors_ab12/db_dumps/schema.sql.gz",
			"dup-installer/dup_descriptors_ab12/db_dumps/wp_options-h2.sql.gz", "dup-installer/dup_descriptors_ab12/orig_files/source_site_wpconfig",
			"dup-installer/dup_descriptors_ab12/orig_files/source_site_htaccess", "wp-content/backup.sql"),
			root: "", sql: "dup-installer/dup_descriptors_ab12/db_dumps", fw: FrameworkWordPress, dup: "dup-installer",
			wpConfig: "dup-installer/dup_descriptors_ab12/orig_files/source_site_wpconfig",
			parts:    []string{"dup-installer/dup_descriptors_ab12/db_dumps/schema.sql.gz", "dup-installer/dup_descriptors_ab12/db_dumps/wp_options-h2.sql.gz", "dup-installer/dup_descriptors_ab12/db_dumps/wp_users-h1.sql.gz"}},
		{name: "Duplicator 1.3 wp-config copy", entries: e("index.php", "wp-includes/version.php", "dup-installer/dup-wp-config-arc__ab12.txt", "dup-installer/dup-database__ab12.sql"),
			root: "", sql: "dup-installer/dup-database__ab12.sql", fw: FrameworkWordPress, dup: "dup-installer", wpConfig: "dup-installer/dup-wp-config-arc__ab12.txt"},
		{name: "WordPress copy inside the site wins by depth", entries: e("old/wp-includes/version.php", "a/b/wp-includes/version.php"),
			root: "old", fw: FrameworkWordPress},
		{name: "Laravel", entries: e("project/artisan", "project/public/index.php", "dump.sql"),
			root: "project", sql: "dump.sql", fw: FrameworkLaravel},
		{name: "plain PHP, two dumps", entries: e("site/index.php", "site/lib/index.php", "small.sql", "big.sql"),
			root: "site", sql: "big.sql", warnings: 1},
		{name: "no site", entries: e("notes.txt", "db.sql"), wantErr: errcode.InvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := planArchive(tt.entries)
			if tt.wantErr != "" {
				if errcode.Code(err) != tt.wantErr {
					t.Fatalf("err = %v, want %s", err, tt.wantErr)
				}
				return
			}
			if err != nil || p.root != tt.root || p.sql != tt.sql || p.framework != tt.fw || len(p.warnings) != tt.warnings ||
				!slices.Equal(p.sqlParts, tt.parts) || p.duplicator != tt.dup || p.wpConfig != tt.wpConfig {
				t.Errorf("plan = %+v, %v", p, err)
			}
		})
	}
}

func TestPHPForArchive(t *testing.T) {
	for wp, want := range map[string]string{"": "8.4", "3.5.1": "5.6", "4.9.8": "7.4", "5.9": "8.1", "6.8.1": "8.4"} {
		if got, _ := phpForArchive(wp); got != want {
			t.Errorf("WordPress %q → PHP %s, want %s", wp, got, want)
		}
	}
}

func TestNameFromArchive(t *testing.T) {
	for file, want := range map[string]string{
		"/x/Shop Backup 2024.zip": "shop-backup-2024", "/x/site.tar.gz": "site", "/x/___.tgz": "imported",
		"/x/my_shop_6f1c2a9b0d3e4f5a1234_20261116120000_archive.zip": "my-shop",
	} {
		if got := nameFromArchive(file); got != want {
			t.Errorf("%s → %q, want %q", file, got, want)
		}
	}
}

func TestArchiveKindDAF(t *testing.T) {
	if _, err := archiveKind("/x/site_archive.daf"); errcode.Code(err) != errcode.Unsupported {
		t.Fatalf("daf = %v", err)
	}
}

func TestExtractArchive(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "site.zip")
	f, _ := os.Create(zipPath)
	zw := zip.NewWriter(f)
	for name, body := range map[string]string{"site/index.php": "<?php", "__MACOSX/site/._index.php": "x", "site/.DS_Store": "x"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(body))
	}
	_ = zw.Close()
	_ = f.Close()
	out := filepath.Join(dir, "out")
	if err := extractArchive(zipPath, "zip", out); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(out, "site", "index.php")); err != nil || string(b) != "<?php" {
		t.Fatalf("index.php = %q, %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(out, "__MACOSX")); err == nil {
		t.Error("macOS metadata extracted")
	}

	// tar.gz with a link and an escaping name.
	tgz := filepath.Join(dir, "bad.tar.gz")
	tf, _ := os.Create(tgz)
	gz := gzip.NewWriter(tf)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "site/link", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	_ = tw.WriteHeader(&tar.Header{Name: "site/index.php", Typeflag: tar.TypeReg, Size: 5, Mode: 0o644})
	_, _ = tw.Write([]byte("<?php"))
	_ = tw.Close()
	_ = gz.Close()
	_ = tf.Close()
	entries, err := listArchive(tgz, "tgz")
	if err != nil || len(entries) != 1 || entries[0].name != "site/index.php" {
		t.Fatalf("tgz entries = %+v, %v", entries, err)
	}
	if _, err := archiveName("../../etc/x"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("escaping name: err = %v", err)
	}
	if _, err := archiveName("/etc/x"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("absolute name: err = %v", err)
	}
}
