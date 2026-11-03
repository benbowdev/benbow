// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestBlueprint creates a WordPress site from a blueprint with a
// wordpress.org plugin (real download), a plugin and a theme from zip URLs
// (served here), a must-use plugin, constants and options; then checks the
// SHA-256 recorded on first use, applying to an existing site, and saving a
// site as a blueprint.
func TestBlueprint(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	var mu sync.Mutex
	zips := map[string][]byte{
		"/covo-own.zip": zipOf(t, map[string]string{
			"covo-own/covo-own.php": "<?php\n/*\nPlugin Name: Covo Own\n*/\nadd_action('wp_head', function () { echo '<meta name=\"covo-own\" content=\"v1\">'; });\n",
		}),
		"/covo-theme.zip": zipOf(t, map[string]string{
			"covo-theme/style.css": "/*\nTheme Name: Covo Theme\n*/\n",
			"covo-theme/index.php": "<?php wp_head(); echo 'covo-theme-home'; ?>",
		}),
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		body, ok := zips[r.URL.Path]
		mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	h := newHarness(t)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")
	root := t.TempDir()

	bp := fmt.Sprintf(`{
  "name": "agency",
  "description": "Covo test",
  "themes": [{"url": %q, "activate": true}],
  "plugins": [{"slug": "hello-dolly"}, {"url": %q}],
  "options": {"permalinks": "/%%postname%%/", "timezone": "Europe/Rome", "remove_sample_content": true,
              "set": {"blogdescription": "Made by Covo"}},
  "constants": {"COVO_TEST": "yes", "WP_DEBUG_LOG": true},
  "mu_plugins": [{"name": "covo-mu.php", "content": "<?php define('COVO_MU', 1);"}]
}`, srv.URL+"/covo-theme.zip", srv.URL+"/covo-own.zip")
	file := filepath.Join(root, "agency.json")
	if err := os.WriteFile(file, []byte(bp), 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("blueprint", "import", file)
	if env := h.covo("blueprint", "import", file); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("second import = %+v", env)
	}
	bad := filepath.Join(root, "bad.json")
	_ = os.WriteFile(bad, []byte(`{"name":"bad","constants":{"DB_PASSWORD":"x"}}`), 0o600)
	if env := h.covo("blueprint", "import", bad); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("bad blueprint = %+v", env)
	}

	// --json never asks: a blueprint needs --yes.
	if env := h.covo("wordpress", "shop", "--path", filepath.Join(root, "shop"), "--blueprint", "agency"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("without --yes = %+v", env)
	}
	var res struct {
		URL       string `json:"url"`
		Blueprint struct {
			Installed []string `json:"installed"`
			Sources   []struct {
				URL    string `json:"url"`
				SHA256 string `json:"sha256"`
			} `json:"sources"`
			Notice string `json:"notice"`
		} `json:"blueprint"`
	}
	data := h.mustCovo("wordpress", "shop", "--path", filepath.Join(root, "shop"), "--blueprint", "agency", "--yes")
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Blueprint.Sources) != 3 || res.Blueprint.Notice == "" {
		t.Fatalf("blueprint result = %s", data)
	}
	checks := [][2]string{
		{"plugin is-active hello-dolly", ""},
		{"plugin is-active covo-own", ""},
		{"option get stylesheet", "covo-theme"},
		{"option get permalink_structure", "/%postname%/"},
		{"option get timezone_string", "Europe/Rome"},
		{"option get blogdescription", "Made by Covo"},
		{"config get COVO_TEST", "yes"},
		{"config get WP_DEBUG_LOG", "1"},
		{"eval echo(defined('COVO_MU')?'mu':'none');", "mu"},
		{"post list --post_type=post,page --name=hello-world --format=count", "0"},
	}
	for _, c := range checks {
		var args []string
		if strings.HasPrefix(c[0], "eval ") {
			args = []string{"eval", strings.TrimPrefix(c[0], "eval ")}
		} else {
			args = strings.Fields(c[0])
		}
		out, code := h.wp(append([]string{"shop"}, args...)...)
		if code != 0 || (c[1] != "" && strings.TrimSpace(out) != c[1]) {
			t.Errorf("wp %s = %q (exit %d), want %q", c[0], out, code, c[1])
		}
	}
	if _, err := os.Stat(filepath.Join(root, "shop", "wp-content", "plugins", "akismet")); !os.IsNotExist(err) {
		t.Errorf("akismet still there: %v", err)
	}
	st := h.status()
	if body := h.get(res.URL, st.CA.CertPath); !strings.Contains(body, "covo-theme-home") || !strings.Contains(body, `content="v1"`) {
		t.Fatalf("home page: %.300q", body)
	}
	var sources []struct{ URL string }
	_ = json.Unmarshal(h.mustCovo("blueprint", "sources"), &sources)
	if len(sources) != 3 {
		t.Fatalf("sources = %+v", sources)
	}

	// Applying to an existing site: dry run, then a changed zip is refused
	// until the change is accepted; a snapshot comes first.
	h.mustCovo("wordpress", "plain", "--path", filepath.Join(root, "plain"))
	var plan struct{ Installed []string }
	_ = json.Unmarshal(h.mustCovo("blueprint", "apply", "agency", "plain", "--dry-run"), &plan)
	if len(plan.Installed) != 4 { // theme, 2 plugins, mu-plugin
		t.Fatalf("dry run = %+v", plan)
	}
	if _, err := os.Stat(filepath.Join(root, "plain", "wp-content", "plugins", "covo-own")); !os.IsNotExist(err) {
		t.Fatal("dry run installed something")
	}
	mu.Lock()
	zips["/covo-own.zip"] = zipOf(t, map[string]string{"covo-own/covo-own.php": "<?php\n/*\nPlugin Name: Covo Own\n*/\n// changed\n"})
	mu.Unlock()
	env := h.covo("blueprint", "apply", "agency", "plain", "--yes")
	if env.OK || env.Error.Code != "checksum_mismatch" || !strings.Contains(env.Error.Message, "changed since") || !strings.Contains(env.Error.Message, "snapshot") {
		t.Fatalf("changed source = %+v", env.Error)
	}
	var applied struct {
		Snapshot string
		Warnings []string
	}
	_ = json.Unmarshal(h.mustCovo("blueprint", "apply", "agency", "plain", "--yes", "--accept-changes"), &applied)
	if applied.Snapshot == "" || len(applied.Warnings) == 0 {
		t.Fatalf("accepted apply = %+v", applied)
	}
	if out, code := h.wp("plain", "plugin", "is-active", "covo-own"); code != 0 {
		t.Fatalf("covo-own on plain: %q", out)
	}

	// Saving a site: wordpress.org plugins pinned, others as warnings.
	var saved struct {
		Warnings []string
	}
	_ = json.Unmarshal(h.mustCovo("blueprint", "save", "shop", "from-shop"), &saved)
	var got struct {
		Plugins []struct{ Slug, Version string }
		Themes  []struct{ Slug string }
		Options struct{ Permalinks, Timezone string }
		MU      []struct{ Name string } `json:"mu_plugins"`
	}
	_ = json.Unmarshal(h.mustCovo("blueprint", "show", "from-shop"), &got)
	if len(got.Plugins) != 1 || got.Plugins[0].Slug != "hello-dolly" || got.Plugins[0].Version == "" ||
		got.Options.Permalinks != "/%postname%/" || got.Options.Timezone != "Europe/Rome" || len(got.MU) != 1 {
		t.Fatalf("saved blueprint = %+v", got)
	}
	if !strings.Contains(strings.Join(saved.Warnings, "\n"), "covo-own") {
		t.Fatalf("warnings = %v", saved.Warnings)
	}
	out := filepath.Join(root, "export.json")
	h.mustCovo("blueprint", "export", "from-shop", out)
	if b, err := os.ReadFile(out); err != nil || !bytes.Contains(b, []byte(`"hello-dolly"`)) {
		t.Fatalf("export: %v", err)
	}
	h.mustCovo("blueprint", "delete", "from-shop")
	var list []struct{ Name string }
	_ = json.Unmarshal(h.mustCovo("blueprint", "list"), &list)
	if len(list) != 1 || list[0].Name != "agency" {
		t.Fatalf("list = %+v", list)
	}
}
