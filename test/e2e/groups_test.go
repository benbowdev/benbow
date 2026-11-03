// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewSiteInGroup adds a site with every option in one step (new folder,
// domain, aliases, group, php.ini value), moves it between nested groups
// and checks a failed add leaves nothing behind.
func TestNewSiteInGroup(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("start")

	var g struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal(h.mustCovo("group", "add", "Clients/Acme"), &g); err != nil || g.Path != "Clients/Acme" {
		t.Fatalf("group add = %+v, %v", g, err)
	}

	dir := filepath.Join(t.TempDir(), "new site")
	var site struct {
		Aliases     []string          `json:"aliases"`
		GroupID     int64             `json:"group_id"`
		PHPSettings map[string]string `json:"php_settings"`
		URL         string            `json:"url"`
	}
	data := h.mustCovo("site", "add", "shop", "--php", "8.4", "--path", dir, "--create-folder",
		"--aliases", "www.shop.localhost", "--group", "clients/acme", "--ini", "memory_limit=1g")
	if err := json.Unmarshal(data, &site); err != nil || site.GroupID != g.ID ||
		strings.Join(site.Aliases, ",") != "www.shop.localhost" || site.PHPSettings["memory_limit"] != "1G" {
		t.Fatalf("site add = %s, %v", data, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(`<?php echo ini_get('memory_limit');`), 0o644); err != nil {
		t.Fatal(err)
	}
	st := h.status()
	if body := h.get(strings.Replace(site.URL, "shop.", "www.shop.", 1), st.CA.CertPath); body != "1G" {
		t.Fatalf("alias served %q", body)
	}

	// A failed add removes the folder it created.
	other := filepath.Join(t.TempDir(), "other")
	for _, args := range [][]string{
		{"site", "add", "other", "--path", other, "--create-folder", "--aliases", "www.shop.localhost"},
		{"site", "add", "other", "--path", other, "--create-folder", "--database", "new"},
		{"site", "add", "other", "--path", other, "--create-folder", "--ini", "short_open_tag=On"},
		{"site", "add", "other", "--path", other, "--create-folder", "--group", "Nope"},
	} {
		if env := h.covo(args...); env.OK {
			t.Errorf("%v succeeded", args)
		}
		if _, err := os.Stat(other); !os.IsNotExist(err) {
			t.Fatalf("%v left %s behind: %v", args, other, err)
		}
	}
	if env := h.covo("site", "add", "other", "--path", dir, "--create-folder"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("create-folder on a folder with files = %+v", env)
	}

	// Moving between groups only changes the list.
	if err := json.Unmarshal(h.mustCovo("site", "set", "shop", "--group", "Clients"), &site); err != nil || site.GroupID == g.ID || site.GroupID == 0 {
		t.Fatalf("move to Clients = %+v, %v", site, err)
	}
	h.mustCovo("group", "move", "Clients/Acme", "/")
	if env := h.covo("group", "move", "Clients", "Clients"); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("move into itself = %+v", env)
	}
	h.mustCovo("group", "remove", "Clients")
	var list struct {
		Sites []struct {
			Name    string `json:"name"`
			GroupID int64  `json:"group_id"`
		} `json:"sites"`
		Groups []struct {
			Path string `json:"path"`
		} `json:"groups"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Sites) != 1 || list.Sites[0].GroupID != 0 || len(list.Groups) != 1 || list.Groups[0].Path != "Acme" {
		t.Fatalf("after removing Clients: %+v", list)
	}
	if body := h.get(site.URL, st.CA.CertPath); body != "1G" {
		t.Fatalf("site after group changes served %q", body)
	}
}
