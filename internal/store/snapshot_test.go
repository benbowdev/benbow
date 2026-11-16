// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"testing"
	"time"
)

// A snapshot keeps the version of the database server its dump came from.
func TestSnapshotDBVersion(t *testing.T) {
	ctx := context.Background()
	s, _ := openTest(t)
	if err := s.AddSite(ctx, Site{Name: "demo", Path: "/Users/a/demo", PHPMinor: "8.4", Domain: "demo.localhost", CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	sn := Snapshot{ID: "1", Site: "demo", CreatedAt: time.Now(), Kind: "manual", DBEngine: "mysql", DBName: "demo", DBFile: "/x.sql.gz", DBVersion: "8.4.13", FilesDir: "/f", Root: "/Users/a/demo"}
	if err := s.AddSnapshot(ctx, sn); err != nil {
		t.Fatal(err)
	}
	got, err := s.Snapshot(ctx, "demo", "1")
	if err != nil || got.DBVersion != "8.4.13" {
		t.Fatalf("Snapshot = %+v, %v", got, err)
	}

	// An agent session's snapshot gets its dump (and version) afterwards.
	late := Snapshot{ID: "2", Site: "demo", CreatedAt: time.Now(), Kind: "mcp", FilesDir: "/f2", Root: "/Users/a/demo"}
	if err := s.AddSnapshot(ctx, late); err != nil {
		t.Fatal(err)
	}
	late.DBEngine, late.DBName, late.DBFile, late.DBVersion = "mysql57", "demo", "/y.sql.gz", "5.7.44"
	if err := s.UpdateSnapshotDB(ctx, late); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Snapshot(ctx, "demo", "2"); err != nil || got.DBVersion != "5.7.44" {
		t.Fatalf("after UpdateSnapshotDB = %+v, %v", got, err)
	}
}
