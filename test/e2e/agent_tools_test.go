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

// TestAgentTools checks the tools agents verify their work with: a page
// fetched with exactly its own PHP errors, the mail PHP sent, and a
// database that can be read but not written.
func TestAgentTools(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("mail", "enable")
	h.mustBenbow("start")
	root := filepath.Join(t.TempDir(), "shop")
	h.mustBenbow("wordpress", "shop", "--path", root)
	page := "<?php\ntrigger_error('benbow deprecated', E_USER_DEPRECATED);\necho $benbow_undefined;\necho 'probe-ok';\n"
	if err := os.WriteFile(filepath.Join(root, "probe.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}

	// fetch: the request's own PHP messages.
	var res struct {
		Status int    `json:"status"`
		URL    string `json:"url"`
		Text   string `json:"text"`
		PHP    []struct {
			Level   string `json:"level"`
			Message string `json:"message"`
			Line    int    `json:"line"`
		} `json:"php"`
		Redirects []struct{ URL string } `json:"redirects"`
	}
	if err := json.Unmarshal(h.mustBenbow("fetch", "shop", "/probe.php"), &res); err != nil {
		t.Fatal(err)
	}
	levels := map[string]bool{}
	for _, m := range res.PHP {
		levels[m.Level] = true
	}
	if res.Status != 200 || !strings.Contains(res.Text, "probe-ok") || len(res.PHP) != 2 || !levels["deprecated"] || !levels["warning"] {
		t.Fatalf("fetch = %+v", res)
	}
	// A normal request afterwards is not sent to a probe log, and the probe
	// log is gone; the site's own log has everything.
	st := h.status()
	if body := h.get(strings.TrimSuffix(res.URL, "/probe.php")+"/probe.php", st.CA.CertPath); !strings.Contains(body, "probe-ok") {
		t.Fatalf("normal request = %q", body)
	}
	if left, _ := filepath.Glob(filepath.Join(h.home(), "tmp", "probes", "*")); len(left) != 0 {
		t.Fatalf("probe logs left: %v", left)
	}
	log, _ := os.ReadFile(filepath.Join(h.home(), "logs", "sites", "shop", "php-error.log"))
	if strings.Count(string(log), "benbow deprecated") != 2 {
		t.Fatalf("site log should have both requests' messages:\n%s", log)
	}
	// Redirects inside Benbow are followed; other addresses refused.
	if err := json.Unmarshal(h.mustBenbow("fetch", "shop", "/wp-admin/"), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || len(res.Redirects) == 0 || !strings.Contains(res.URL, "wp-login.php") {
		t.Fatalf("wp-admin = %+v", res)
	}
	if env := h.benbow("fetch", "shop", "https://example.com/"); env.OK || env.Error.Code != "forbidden" {
		t.Fatalf("external fetch = %+v", env)
	}

	// Mail sent by PHP.
	if out, code := h.wp("shop", "eval", "wp_mail('buyer@example.com', 'Benbow order 42', 'Confirm: https://shop.localhost/confirm?t=1');"); code != 0 {
		t.Fatalf("wp_mail: %s", out)
	}
	var list []struct {
		ID      string   `json:"id"`
		Subject string   `json:"subject"`
		To      []string `json:"to"`
	}
	if err := json.Unmarshal(h.mustBenbow("mail", "list", "--to", "buyer@example.com"), &list); err != nil || len(list) != 1 || list[0].Subject != "Benbow order 42" {
		t.Fatalf("mail list = %+v, %v", list, err)
	}
	var msg struct {
		Text  string   `json:"text"`
		Links []string `json:"links"`
	}
	if err := json.Unmarshal(h.mustBenbow("mail", "show", list[0].ID), &msg); err != nil || len(msg.Links) != 1 || msg.Links[0] != "https://shop.localhost/confirm?t=1" {
		t.Fatalf("mail show = %+v, %v", msg, err)
	}

	// Read-only database.
	var q struct {
		Columns []string   `json:"columns"`
		Rows    [][]string `json:"rows"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "query", "shop", "SELECT option_value FROM wp_options WHERE option_name = 'blogname'"), &q); err != nil ||
		len(q.Rows) != 1 || q.Columns[0] != "option_value" {
		t.Fatalf("query = %+v, %v", q, err)
	}
	count := func() string {
		_ = json.Unmarshal(h.mustBenbow("db", "query", "shop", "SELECT COUNT(*) AS n FROM wp_options"), &q)
		return q.Rows[0][0]
	}
	before := count()
	if env := h.benbow("db", "query", "shop", "DELETE FROM wp_options"); env.OK || env.Error.Code != "forbidden" {
		t.Fatalf("DELETE = %+v", env)
	}
	// Past the statement check (WITH … DELETE is valid MySQL): the
	// read-only user and transaction still refuse it.
	if env := h.benbow("db", "query", "shop", "WITH x AS (SELECT 1) DELETE FROM wp_options"); env.OK {
		t.Fatalf("WITH … DELETE ran: %+v", env)
	}
	if after := count(); after != before {
		t.Fatalf("rows %s → %s: something was written", before, after)
	}
}
