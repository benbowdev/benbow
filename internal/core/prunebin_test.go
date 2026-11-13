// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/components"
	"github.com/benbowdev/benbow/internal/store"
)

// After an update only the version in use and the previous one stay, for
// pinned components and for each PHP minor.
func TestPruneOldVersions(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "benbow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	bin := filepath.Join(dir, "bin")
	c := &Core{Store: st, Installer: components.Installer{BinDir: bin}}
	mk := func(parts ...string) string {
		p := filepath.Join(append([]string{bin}, parts...)...)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, 8192), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, v := range []string{"2.11.5", "2.11.7", "2.11.9"} {
		mk("caddy", v)
	}
	mk("mailpit", "1.33.0") // never updated: no previous version recorded
	mk("mailpit", "1.32.0") // left over, older than the pinned one
	set := map[string]string{"pin_caddy": "2.11.9", "previous_caddy": "2.11.7", "pin_mailpit": "1.33.0"}
	for k, v := range set {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatal(err)
		}
	}
	mk("redis", "8.10.3") // no pin: never touched
	mk("redis", "8.10.2")
	for _, v := range []string{"8.4.21", "8.4.22", "8.4.23", "8.1.34"} {
		if err := st.PutPHP(ctx, store.PHPVersion{Version: v, Minor: v[:3], Path: mk("php", v), InstalledAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}

	if freed := c.pruneOldVersions(ctx); freed <= 0 {
		t.Errorf("freed = %d", freed)
	}
	ls := func(comp string) []string {
		entries, _ := os.ReadDir(filepath.Join(bin, comp))
		var out []string
		for _, e := range entries {
			out = append(out, e.Name())
		}
		return out
	}
	for comp, want := range map[string][]string{
		"caddy":   {"2.11.7", "2.11.9"},
		"mailpit": {"1.33.0"},
		"redis":   {"8.10.2", "8.10.3"},
		"php":     {"8.1.34", "8.4.22", "8.4.23"},
	} {
		if got := ls(comp); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", comp, got, want)
		}
	}
	php, _ := st.ListPHP(ctx)
	var versions []string
	for _, v := range php {
		versions = append(versions, v.Version)
	}
	slices.Sort(versions)
	if want := []string{"8.1.34", "8.4.22", "8.4.23"}; !slices.Equal(versions, want) {
		t.Errorf("PHP versions recorded = %v, want %v", versions, want)
	}
}
