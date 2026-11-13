// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestClone copies a WordPress site under a new name: folder (with .git),
// its own database, wp-config.php and the address; the source is left as
// it was. --same-folder only adds a name.
func TestClone(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")
	parent := t.TempDir()
	src := filepath.Join(parent, "shop")
	h.mustBenbow("wordpress", "shop", "--path", src)
	if err := os.MkdirAll(filepath.Join(src, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcConfig, err := os.ReadFile(filepath.Join(src, "wp-config.php"))
	if err != nil {
		t.Fatal(err)
	}

	var res struct {
		Name     string   `json:"name"`
		URL      string   `json:"url"`
		Folder   string   `json:"folder"`
		Database string   `json:"database"`
		URLFrom  string   `json:"url_from"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "clone", "shop", "shop-copy"), &res); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(parent, "shop-copy")
	if res.Folder != dst || res.Database == "" || res.Database == "shop" || !strings.Contains(res.URLFrom, "shop.localhost") {
		t.Fatalf("clone = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git", "HEAD")); err != nil {
		t.Errorf(".git not cloned: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dst, "wp-config.php"))
	if err != nil || !strings.Contains(string(cfg), "'"+res.Database+"'") {
		t.Errorf("clone wp-config.php = %v\n%s", err, cfg)
	}
	if after, _ := os.ReadFile(filepath.Join(src, "wp-config.php")); string(after) != string(srcConfig) {
		t.Error("the source's wp-config.php changed")
	}

	home := func(site string) string {
		var q struct {
			Rows [][]string `json:"rows"`
		}
		if err := json.Unmarshal(h.mustBenbow("db", "query", site, "SELECT option_value FROM wp_options WHERE option_name = 'home'"), &q); err != nil || len(q.Rows) != 1 {
			t.Fatalf("query %s = %+v, %v", site, q, err)
		}
		return q.Rows[0][0]
	}
	if got := home("shop-copy"); got != res.URL {
		t.Errorf("clone home = %q, want %q", got, res.URL)
	}
	if got := home("shop"); !strings.Contains(got, "//shop.localhost") {
		t.Errorf("source home = %q", got)
	}
	st := h.status()
	if body := h.get(res.URL+"/", st.CA.CertPath); !strings.Contains(body, "shop-copy.localhost") {
		t.Errorf("clone page does not link to its own address:\n%.500s", body)
	}

	// Name taken, folder taken.
	if env := h.benbow("site", "clone", "shop", "shop-copy"); env.OK || env.Error.Code != "conflict" {
		t.Errorf("clone onto an existing site = %+v", env)
	}
	if env := h.benbow("site", "clone", "shop", "other", "--folder", dst); env.OK || env.Error.Code != "conflict" {
		t.Errorf("clone into an existing folder = %+v", env)
	}

	// Same folder: a new name, nothing copied.
	var same struct {
		Path     string `json:"path"`
		Database string `json:"database"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "clone", "shop", "shop-82", "--same-folder", "--php", "8.2"), &same); err != nil {
		t.Fatal(err)
	}
	if same.Path != src || same.Database != "" {
		t.Errorf("same folder = %+v", same)
	}

	// Removing the clone and its database leaves the source working.
	h.mustBenbow("site", "remove", "shop-copy", "--drop-database", "--yes")
	if got := home("shop"); !strings.Contains(got, "//shop.localhost") {
		t.Errorf("source home after removing the clone = %q", got)
	}
}
