// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// raw runs benbow without --json (tools pass their output through).
func (h *harness) raw(args ...string) (string, int) {
	h.t.Helper()
	cmd := exec.Command(h.bin, args...)
	cmd.Env = h.env
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			h.t.Fatalf("benbow %v: %v", args, err)
		}
		code = exit.ExitCode()
	}
	return string(out), code
}

// TestLaravel creates a Laravel project on Benbow's MySQL, serves it from
// public/, runs artisan and composer, and recognises an existing project.
func TestLaravel(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("start")

	dir := filepath.Join(t.TempDir(), "blog")
	var res struct {
		URL       string `json:"url"`
		Path      string `json:"path"`
		AppRoot   string `json:"app_root"`
		Framework string `json:"framework"`
		Database  string `json:"database"`
		Laravel   string `json:"laravel"`
	}
	data := h.mustBenbow("laravel", "blog", "--path", dir, "--php", "8.4")
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Framework != "laravel" || res.AppRoot != dir || res.Path != filepath.Join(dir, "public") || res.Database != "blog" || res.Laravel == "" {
		t.Fatalf("laravel = %s", data)
	}
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"APP_URL=" + res.URL, "DB_CONNECTION=mysql", "DB_DATABASE=blog", "DB_USERNAME=blog"} {
		if !strings.Contains(string(env), want+"\n") {
			t.Errorf(".env lacks %q", want)
		}
	}

	st := h.status()
	if body := h.get(res.URL, st.CA.CertPath); !strings.Contains(body, "<title>blog</title>") {
		t.Fatalf("home page: %.300q", body)
	}
	out, code := h.raw("artisan", "blog", "migrate:status")
	if code != 0 || !strings.Contains(out, "create_users_table") || !strings.Contains(out, "Ran") {
		t.Fatalf("artisan migrate:status (%d):\n%s", code, out)
	}
	out, code = h.raw("composer", "blog", "show", "laravel/framework", "--no-ansi")
	if code != 0 || !strings.Contains(out, "versions") {
		t.Fatalf("composer show (%d):\n%s", code, out)
	}
	if out, code = h.raw("artisan", "blog", "no:such-command"); code == 0 {
		t.Fatalf("a failing artisan command exited 0:\n%s", out)
	}

	// An existing project chosen by its root is served from public/.
	var added struct {
		Path      string `json:"path"`
		AppRoot   string `json:"app_root"`
		Framework string `json:"framework"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "add", "blog2", "--path", dir), &added); err != nil {
		t.Fatal(err)
	}
	if added.Framework != "laravel" || added.Path != filepath.Join(dir, "public") || added.AppRoot != dir {
		t.Fatalf("site add of a Laravel root = %+v", added)
	}
	if out, code := h.raw("artisan", "nope", "list"); code == 0 || !strings.Contains(out, "not found") {
		t.Fatalf("artisan on a missing site (%d): %s", code, out)
	}
	if env := h.benbow("laravel", "old", "--php", "8.1", "--path", filepath.Join(t.TempDir(), "old")); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("laravel on PHP 8.1 = %+v", env)
	}
}
