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

const legacyCode = `<?php
$a = array(1, 2);
while (list($k, $v) = each($a)) {
	echo $k;
}
$f = create_function('$x', 'return $x;');
mysql_connect('localhost');
`

// TestPHPCheck finds what breaks on newer PHP in legacy code, and only in
// wp-content for a WordPress site.
func TestPHPCheck(t *testing.T) {
	h := newHarness(t)
	dir := newSite(t)
	if err := os.WriteFile(filepath.Join(dir, "legacy.php"), []byte(legacyCode), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustBenbow("site", "add", "old", "--php", "8.4", "--path", dir)

	type result struct {
		Target    string   `json:"target"`
		WordPress bool     `json:"wordpress"`
		Scanned   []string `json:"scanned"`
		Errors    int      `json:"errors"`
		Files     []struct {
			Path     string `json:"path"`
			Messages []struct {
				Line    int    `json:"line"`
				Type    string `json:"type"`
				Message string `json:"message"`
			} `json:"messages"`
		} `json:"files"`
	}
	var r result
	data := h.mustBenbow("php", "check", "old", "--target", "8.4")
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	if r.Target != "8.4" || r.WordPress || r.Errors < 3 || len(r.Files) != 1 || r.Files[0].Path != "legacy.php" {
		t.Fatalf("check = %s", data)
	}
	var all strings.Builder
	for _, m := range r.Files[0].Messages {
		all.WriteString(m.Message + "\n")
	}
	for _, want := range []string{"each()", "create_function()", "mysql_connect()"} {
		if !strings.Contains(all.String(), want) {
			t.Errorf("no message about %s in:\n%s", want, all.String())
		}
	}

	// A WordPress layout: only wp-content is checked.
	wp := newSite(t)
	for _, f := range []string{"wp-includes/version.php", "wp-content/plugins/old/old.php", "wp-admin/old.php"} {
		p := filepath.Join(wp, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(legacyCode), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.mustBenbow("site", "add", "wp", "--php", "8.4", "--path", wp)
	r = result{}
	if err := json.Unmarshal(h.mustBenbow("php", "check", "wp"), &r); err != nil {
		t.Fatal(err)
	}
	if !r.WordPress || strings.Join(r.Scanned, ",") != "wp-content" || len(r.Files) != 1 || r.Files[0].Path != "wp-content/plugins/old/old.php" {
		t.Fatalf("wordpress check = %+v", r)
	}
	if env := h.benbow("php", "check", "nope"); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("missing site = %+v", env)
	}
}
