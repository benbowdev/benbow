// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/benbowdev/benbow/internal/components"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
)

// DiskUsage puts every folder of the home under one line, once.
func TestDiskUsage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(root, "benbow.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	layout := paths.Layout{Root: root, Bin: filepath.Join(root, "bin"), Data: filepath.Join(root, "data")}
	c := &Core{Store: st, Layout: layout, Installer: components.Installer{BinDir: layout.Bin}}
	write := func(rel string, n int) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("bin/php/8.4.23/php-fpm", 1<<20)
	write("bin/php-cli/8.4.23/php", 1<<20)
	write("bin/php/8.1.34/php-fpm", 1<<20)
	write("bin/caddy/2.11.9/caddy", 1<<20)
	write("data/mysql/ibdata1", 2<<20)
	write("data/wp-cli/cache.zip", 1<<20)
	write("logs/caddy.log", 1<<20)

	rep, err := c.DiskUsage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	var sum int64
	for _, p := range rep.Parts {
		got[p.Name] = p.Bytes >> 20
		sum += p.Bytes
	}
	for name, mb := range map[string]int64{"php": 3, "servers": 1, "databases": 2, "caches": 1, "logs": 1} {
		if got[name] != mb {
			t.Errorf("%s = %d MB, want %d (%+v)", name, got[name], mb, rep.Parts)
		}
	}
	if sum != rep.Total {
		t.Errorf("total %d, parts add up to %d", rep.Total, sum)
	}
	if php := rep.Parts[0]; len(php.Parts) != 2 || php.Parts[0].Title != "PHP 8.4" || php.Parts[0].Bytes>>20 != 2 {
		t.Errorf("PHP parts = %+v", php.Parts)
	}
}
