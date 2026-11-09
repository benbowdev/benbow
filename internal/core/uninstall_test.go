// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/paths"
)

func TestCheckRemovable(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		root string
		ok   bool
	}{
		{home + "/Library/Application Support/benbow", true},
		{t.TempDir(), true},
		{"/", false},
		{home, false},
		{"/Users", false},
	}
	for _, tt := range tests {
		err := checkRemovable(tt.root)
		if tt.ok && err != nil {
			t.Errorf("checkRemovable(%q) = %v", tt.root, err)
		}
		if !tt.ok && errcode.Code(err) != errcode.ForbiddenFolder {
			t.Errorf("checkRemovable(%q) = %v, want forbidden_folder", tt.root, err)
		}
	}
}

func TestMigrationBackups(t *testing.T) {
	l := paths.Layout{Data: t.TempDir()}
	if got := migrationBackups(l); got != nil {
		t.Fatalf("no backups dir = %v", got)
	}
	writeFile(t, filepath.Join(l.Data, "backups", "shop", "wp-config.php.20261104"), "x")
	writeFile(t, filepath.Join(l.Data, "backups", "agents", ".claude.json.20261104"), "x")
	if err := os.MkdirAll(filepath.Join(l.Data, "backups", "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	got := migrationBackups(l)
	if len(got) != 1 || filepath.Base(got[0]) != "shop" {
		t.Fatalf("migrationBackups = %v", got)
	}
}

// A test installation (own label prefix) never touches the real agents'
// configuration.
func TestUninstallerLeavesRealAgentsOfTestInstalls(t *testing.T) {
	t.Setenv("BENBOW_LABEL_PREFIX", "test.benbow.uninstall")
	t.Setenv("BENBOW_AGENTS_HOME", "")
	t.Setenv("BENBOW_HOME", t.TempDir())
	u, err := NewUninstaller()
	if err != nil {
		t.Fatal(err)
	}
	if u.Agents != nil {
		t.Fatalf("agents env = %+v, want none", u.Agents)
	}
}
