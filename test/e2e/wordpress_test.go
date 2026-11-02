// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// wp runs `covo wp` and returns its raw output and exit code.
func (h *harness) wp(args ...string) (string, int) {
	h.t.Helper()
	cmd := exec.Command(h.bin, append([]string{"wp"}, args...)...)
	cmd.Env = h.env
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return out.String(), exit.ExitCode()
	}
	if err != nil {
		h.t.Fatal(err)
	}
	return out.String(), 0
}

func TestWordPress(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	root := t.TempDir()

	// MySQL off: refused before anything is created.
	dir := filepath.Join(root, "My Blog")
	if env := h.covo("wordpress", "blog", "--path", dir); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("without MySQL = %+v", env)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("folder created by a refused request: %v", err)
	}
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")

	// A folder with files is refused and left alone.
	busy := filepath.Join(root, "busy")
	if err := os.MkdirAll(busy, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busy, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if env := h.covo("wordpress", "busy", "--path", busy); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("non-empty folder = %+v", env)
	}
	if _, err := os.Stat(filepath.Join(busy, "keep.txt")); err != nil {
		t.Fatalf("file in a refused folder: %v", err)
	}

	var res struct {
		URL           string `json:"url"`
		Path          string `json:"path"`
		WordPress     string `json:"wordpress"`
		Database      string `json:"database"`
		DBUser        string `json:"database_user"`
		AdminURL      string `json:"admin_url"`
		AdminUser     string `json:"admin_user"`
		AdminPassword string `json:"admin_password"`
	}
	data := h.mustCovo("wordpress", "blog", "--path", dir, "--title", "Covo Test Blog", "--php", "8.2")
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatal(err)
	}
	if res.Path != dir || res.Database != "blog" || res.DBUser != "blog" || res.AdminUser != "admin" ||
		len(res.AdminPassword) != 24 || res.WordPress != "7.1.4" || res.AdminURL != res.URL+"/wp-admin/" {
		t.Fatalf("result = %+v", res)
	}
	st := h.status()
	if body := h.get(res.URL, st.CA.CertPath); !strings.Contains(body, "Covo Test Blog") {
		t.Fatalf("home page: %.300q", body)
	}
	if body := h.get(res.URL+"/wp-login.php", st.CA.CertPath); !strings.Contains(body, "user_login") {
		t.Fatalf("login page: %.300q", body)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "wp-config.php"))
	if err != nil || !strings.Contains(string(cfg), "'DB_USER', 'blog'") || !strings.Contains(string(cfg), "'DB_HOST', 'localhost'") {
		t.Fatalf("wp-config.php: %v", err)
	}

	// wp-cli runs with the site's PHP and reaches its database.
	if out, code := h.wp("blog", "option", "get", "siteurl"); code != 0 || strings.TrimSpace(out) != res.URL {
		t.Fatalf("siteurl = %q (exit %d)", out, code)
	}
	if out, code := h.wp("blog", "eval", "echo PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION;"); code != 0 || strings.TrimSpace(out) != "8.2" {
		t.Fatalf("wp-cli PHP = %q (exit %d)", out, code)
	}
	if out, code := h.wp("blog", "user", "check-password", "admin", res.AdminPassword); code != 0 {
		t.Fatalf("admin password rejected: %q (exit %d)", out, code)
	}
	if _, code := h.wp("blog", "option", "get", "covo_missing_option"); code == 0 {
		t.Fatal("wp-cli exit code not passed through")
	}
	if _, code := h.wp("nope", "option", "get", "siteurl"); code == 0 {
		t.Fatal("wp for a missing site succeeded")
	}
	// wp-cli keeps its cache inside the Covo folder.
	if _, err := os.Stat(filepath.Join(homeOf(t, h), "data", "wp-cli")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	if env := h.covo("wordpress", "blog", "--path", filepath.Join(root, "again")); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("second blog = %+v", env)
	}
	if _, err := os.Stat(filepath.Join(root, "again")); !os.IsNotExist(err) {
		t.Fatalf("folder left by a refused request: %v", err)
	}
}
