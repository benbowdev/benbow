// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTwoPHPVersionsInParallel is the spike's acceptance test: two sites on
// different PHP versions served at the same time by one Caddy.
func TestTwoPHPVersionsInParallel(t *testing.T) {
	h := newHarness(t)
	urls := map[string]string{}
	for _, s := range []struct{ name, php string }{{"demo", "8.4"}, {"legacy", "8.2"}} {
		var added struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(h.mustBenbow("site", "add", s.name, "--php", s.php, "--path", newSite(t)), &added); err != nil {
			t.Fatal(err)
		}
		urls[s.php] = added.URL
	}
	h.mustBenbow("start")

	st := h.status()
	var names []string
	for _, svc := range st.Services {
		if !svc.Running || svc.PID == 0 {
			t.Errorf("service %s not running", svc.Name)
		}
		names = append(names, svc.Name)
	}
	if got := strings.Join(names, ","); got != "caddy,php-8.2,php-8.4" {
		t.Fatalf("services = %s, want caddy,php-8.2,php-8.4", got)
	}

	for php, url := range urls {
		if body := h.get(url, st.CA.CertPath); !strings.HasPrefix(body, php+".") {
			t.Errorf("%s served %q, want PHP %s.x", url, body, php)
		}
	}

	// Switching demo to PHP 8.2 takes effect at once and retires php-8.4.
	h.mustBenbow("site", "set", "demo", "--php", "8.2")
	st = h.status()
	names = nil
	for _, svc := range st.Services {
		names = append(names, svc.Name)
	}
	if got := strings.Join(names, ","); got != "caddy,php-8.2" {
		t.Fatalf("services after set = %s, want caddy,php-8.2", got)
	}
	if body := h.get(urls["8.4"], st.CA.CertPath); !strings.HasPrefix(body, "8.2.") {
		t.Errorf("demo served %q after set, want PHP 8.2.x", body)
	}
	if env := h.benbow("site", "set", "demo", "--path", t.TempDir()); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("set to an empty folder = %+v, want confirmation_required", env)
	}
	if env := h.benbow("site", "set", "nope", "--php", "8.2"); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("set missing site = %+v, want not_found", env)
	}

	// Disabled sites are not served and need no PHP.
	h.mustBenbow("site", "disable", "demo")
	h.mustBenbow("site", "disable", "legacy")
	if got := serviceNames(h.status()); got != "caddy" {
		t.Fatalf("services with every site disabled = %s, want caddy", got)
	}
	h.mustBenbow("site", "enable", "demo")
	st = h.status()
	if got := serviceNames(st); got != "caddy,php-8.2" {
		t.Fatalf("services after enable = %s, want caddy,php-8.2", got)
	}
	if body := h.get(urls["8.4"], st.CA.CertPath); !strings.HasPrefix(body, "8.2.") {
		t.Errorf("demo served %q after enable, want PHP 8.2.x", body)
	}

	// Archived: off and kept apart; it cannot be turned on until unarchived.
	h.mustBenbow("site", "archive", "demo")
	if got := serviceNames(h.status()); got != "caddy" {
		t.Fatalf("services with demo archived = %s, want caddy", got)
	}
	if env := h.benbow("site", "enable", "demo"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("enable an archived site = %+v", env)
	}
	var back struct {
		Enabled  bool `json:"enabled"`
		Archived bool `json:"archived"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "unarchive", "demo"), &back); err != nil || !back.Enabled || back.Archived {
		t.Fatalf("unarchive = %+v, %v", back, err)
	}
	if body := h.get(urls["8.4"], st.CA.CertPath); !strings.HasPrefix(body, "8.2.") {
		t.Errorf("demo served %q after unarchive", body)
	}
}

func serviceNames(st statusData) string {
	var names []string
	for _, svc := range st.Services {
		names = append(names, svc.Name)
	}
	return strings.Join(names, ",")
}
