// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestPHPSettings changes php.ini values while Covo runs and checks that
// PHP-FPM serves the new values at once.
func TestPHPSettings(t *testing.T) {
	h := newHarness(t)
	dir := newSite(t)
	script := `<?php echo ini_get('memory_limit'), ' ', ini_get('post_max_size'), ' ', ini_get('max_execution_time'), ' ', ini_get('display_errors') ?: 'off';`
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	var added struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", dir), &added); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("start")
	st := h.status()
	if got := h.get(added.URL, st.CA.CertPath); got != "512M 128M 300 1" {
		t.Fatalf("defaults served %q", got)
	}

	var res struct {
		MemoryLimit string `json:"memory_limit"`
		Changed     bool   `json:"changed"`
		Applied     bool   `json:"applied"`
	}
	data := h.mustCovo("php", "settings", "8.4", "memory_limit=1g", "upload_max_filesize=64M", "max_execution_time=0", "display_errors=off")
	if err := json.Unmarshal(data, &res); err != nil || res.MemoryLimit != "1G" || !res.Changed || !res.Applied {
		t.Fatalf("settings result = %+v, %v", res, err)
	}
	if got := h.get(added.URL, st.CA.CertPath); got != "1G 64M 0 off" {
		t.Fatalf("after change served %q, want 1G 64M 0 off", got)
	}

	if env := h.covo("php", "settings", "8.4", "memory_limit=1G", "upload_max_filesize=lots"); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("invalid value = %+v, want invalid_input", env)
	}
	if got := h.get(added.URL, st.CA.CertPath); got != "1G 64M 0 off" {
		t.Fatalf("a rejected change altered PHP: %q", got)
	}

	// Per site: demo gets its own values, other keeps the version's, and
	// neither leaks into the other through a shared PHP-FPM worker.
	other := newSite(t)
	if err := os.WriteFile(filepath.Join(other, "index.php"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	var otherSite struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "add", "other", "--php", "8.4", "--path", other), &otherSite); err != nil {
		t.Fatal(err)
	}
	var site struct {
		MemoryLimit string            `json:"memory_limit"`
		Overrides   map[string]string `json:"overrides"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "php", "demo", "memory_limit=256m", "display_errors=on"), &site); err != nil ||
		site.MemoryLimit != "256M" || site.Overrides["memory_limit"] != "256M" || site.Overrides["display_errors"] != "on" {
		t.Fatalf("site php = %+v, %v", site, err)
	}
	for range 5 {
		if got := h.get(added.URL, st.CA.CertPath); got != "256M 64M 0 1" {
			t.Fatalf("demo with its own values served %q", got)
		}
		if got := h.get(otherSite.URL, st.CA.CertPath); got != "1G 64M 0 off" {
			t.Fatalf("other site served %q, want the PHP version's values", got)
		}
	}
	if env := h.covo("site", "php", "demo", "short_open_tag=1"); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("unknown setting = %+v", env)
	}
	h.mustCovo("site", "php", "demo", "memory_limit=", "display_errors=")
	if got := h.get(added.URL, st.CA.CertPath); got != "1G 64M 0 off" {
		t.Fatalf("after reset demo served %q", got)
	}
}
