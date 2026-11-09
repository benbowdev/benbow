// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"
)

// TestOldestAndNewestPHP serves the oldest and newest PHP versions Benbow
// offers side by side, and warns about the one php.net no longer fixes.
func TestOldestAndNewestPHP(t *testing.T) {
	h := newHarness(t)
	urls := map[string]string{}
	minors := []string{"8.0", "8.5"}
	if runtime.GOARCH == "arm64" {
		minors = append(minors, "5.6", "7.4") // benbow-binaries' legacy builds
	}
	for _, minor := range minors {
		var res struct {
			URL     string `json:"url"`
			Warning string `json:"warning"`
		}
		name := "php" + strings.ReplaceAll(minor, ".", "")
		if err := json.Unmarshal(h.mustBenbow("site", "add", name, "--php", minor, "--path", newSite(t)), &res); err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(res.Warning, "no security fixes"); got != (minor != "8.5") {
			t.Errorf("PHP %s warning = %q", minor, res.Warning)
		}
		urls[minor] = res.URL
	}
	h.mustBenbow("start")
	st := h.status()
	for minor, url := range urls {
		if body := h.get(url, st.CA.CertPath); !strings.HasPrefix(body, minor+".") {
			t.Errorf("PHP %s site served %q", minor, body)
		}
	}
}
