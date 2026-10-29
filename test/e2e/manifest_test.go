// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"bufio"
	"net/http"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/components"
)

// TestCaddyManifestMatchesOfficialChecksums checks every pinned Caddy SHA-512
// against the checksums.txt published with the official release.
func TestCaddyManifestMatchesOfficialChecksums(t *testing.T) {
	m, err := components.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	official := map[string]map[string]string{} // version -> file -> sha512
	for _, a := range m.Artifacts {
		if a.Component != "caddy" {
			continue
		}
		if a.SHA512 == "" {
			t.Errorf("caddy %s/%s has no sha512", a.Version, a.Arch)
			continue
		}
		sums, ok := official[a.Version]
		if !ok {
			sums = fetchCaddyChecksums(t, a.Version)
			official[a.Version] = sums
		}
		file := a.URL[strings.LastIndex(a.URL, "/")+1:]
		if got := sums[file]; got != a.SHA512 {
			t.Errorf("%s: manifest sha512 %s, official %s", file, a.SHA512, got)
		}
	}
}

func fetchCaddyChecksums(t *testing.T, version string) map[string]string {
	t.Helper()
	url := "https://github.com/caddyserver/caddy/releases/download/v" + version + "/caddy_" + version + "_checksums.txt"
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, resp.Status)
	}
	sums := map[string]string{}
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 {
			sums[fields[1]] = fields[0]
		}
	}
	return sums
}
