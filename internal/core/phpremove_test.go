// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
)

func TestRemovePHP(t *testing.T) {
	ctx := context.Background()
	c := runCore(t)
	var dirs []string
	for _, v := range []string{"8.3.20", "8.3.21", "8.2.30"} {
		dir := filepath.Join(c.Layout.Bin, "php", v)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "php-fpm"), make([]byte, 10000), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := c.Store.PutPHP(ctx, store.PHPVersion{Version: v, Minor: minorOf(v), Path: dir, InstalledAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, dir)
	}
	conf := filepath.Join(c.Layout.Etc, "php", "8.3")
	if err := os.MkdirAll(conf, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.AddSite(ctx, store.Site{Name: "old", Path: "/x", PHPMinor: "8.3", Domain: "old.localhost", Archived: true, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	if _, err := c.RemovePHP(ctx, "7.1"); errcode.Code(err) != errcode.NotFound {
		t.Errorf("not installed: err = %v", err)
	}
	if _, err := c.RemovePHP(ctx, "8"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("bad minor: err = %v", err)
	}
	if _, err := c.RemovePHP(ctx, "8.3"); errcode.Code(err) != errcode.Conflict {
		t.Fatalf("used by an archived site: err = %v", err)
	}
	if _, err := os.Stat(dirs[0]); err != nil {
		t.Fatal("files removed although the version is in use")
	}
	if err := c.Store.RemoveSite(ctx, "old"); err != nil {
		t.Fatal(err)
	}
	res, err := c.RemovePHP(ctx, "8.3")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Versions) != 2 || res.Freed < 20000 {
		t.Errorf("removed = %+v", res)
	}
	for _, d := range []string{dirs[0], dirs[1], conf} {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("%s kept", d)
		}
	}
	if _, err := os.Stat(dirs[2]); err != nil {
		t.Error("another minor's files removed")
	}
	left, _ := c.Store.ListPHP(ctx)
	if len(left) != 1 || left[0].Minor != "8.2" {
		t.Errorf("left = %+v", left)
	}
}
