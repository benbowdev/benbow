// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestOldestAndNewestPHP serves the oldest and newest PHP versions Covo
// offers side by side, and warns about the one php.net no longer fixes.
func TestOldestAndNewestPHP(t *testing.T) {
	h := newHarness(t)
	urls := map[string]string{}
	for _, minor := range []string{"8.0", "8.5"} {
		var res struct {
			URL     string `json:"url"`
			Warning string `json:"warning"`
		}
		name := "php" + strings.ReplaceAll(minor, ".", "")
		if err := json.Unmarshal(h.mustCovo("site", "add", name, "--php", minor, "--path", newSite(t)), &res); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(res.Warning, "no security fixes"); got != (minor == "8.0") {
			t.Errorf("PHP %s warning = %q", minor, res.Warning)
		}
		urls[minor] = res.URL
	}
	h.mustCovo("start")
	st := h.status()
	for minor, url := range urls {
		if body := h.get(url, st.CA.CertPath); !strings.HasPrefix(body, minor+".") {
			t.Errorf("PHP %s site served %q", minor, body)
		}
	}
}
