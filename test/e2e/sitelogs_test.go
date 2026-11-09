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
	"time"
)

type siteLog struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
}

func (h *harness) siteLog(args ...string) siteLog {
	h.t.Helper()
	var l siteLog
	if err := json.Unmarshal(h.mustBenbow(append([]string{"site", "logs"}, args...)...), &l); err != nil {
		h.t.Fatal(err)
	}
	return l
}

// TestSiteLogs checks that each site gets its own access and PHP error log.
func TestSiteLogs(t *testing.T) {
	h := newHarness(t)
	noisy := newSite(t)
	script := `<?php trigger_error("benbow-test-warning", E_USER_WARNING); echo PHP_VERSION;`
	if err := os.WriteFile(filepath.Join(noisy, "index.php"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustBenbow("site", "add", "noisy", "--php", "8.4", "--path", noisy)
	h.mustBenbow("site", "add", "quiet", "--php", "8.4", "--path", newSite(t))
	h.mustBenbow("start")
	st := h.status()
	for _, s := range st.Sites {
		h.get(s.URL, st.CA.CertPath)
	}

	// Caddy writes access logs asynchronously.
	var access siteLog
	for range 50 {
		if access = h.siteLog("noisy"); len(access.Lines) > 0 {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if len(access.Lines) == 0 || !strings.Contains(access.Lines[len(access.Lines)-1], "  200  GET  noisy.localhost/  ") {
		t.Fatalf("noisy access log = %q", access.Lines)
	}
	php := h.siteLog("noisy", "--php")
	if !strings.Contains(strings.Join(php.Lines, "\n"), "benbow-test-warning") {
		t.Fatalf("noisy PHP log = %q (%s)", php.Lines, php.Path)
	}
	if !strings.Contains(php.Path, "Benbow Home") {
		t.Fatalf("PHP log path %s is not under the test home", php.Path)
	}
	if quiet := h.siteLog("quiet", "--php"); len(quiet.Lines) != 0 {
		t.Fatalf("quiet PHP log got another site's errors: %q", quiet.Lines)
	}

	h.mustBenbow("site", "logs", "noisy", "--clear")
	if l := h.siteLog("noisy", "--php"); len(l.Lines) != 0 {
		t.Fatalf("after clear: %q", l.Lines)
	}
	if env := h.benbow("site", "logs", "nope"); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("logs of a missing site = %+v", env)
	}

	h.mustBenbow("site", "remove", "noisy")
	if _, err := os.Stat(filepath.Dir(php.Path)); !os.IsNotExist(err) {
		t.Fatalf("log folder left after remove: %v", err)
	}
}
