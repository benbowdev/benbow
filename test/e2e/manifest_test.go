// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/components"
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

// TestCABundleMatchesPublishedChecksum checks the pinned Mozilla CA bundle
// against the SHA-256 curl.se publishes next to it.
func TestCABundleMatchesPublishedChecksum(t *testing.T) {
	m, err := components.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range m.Artifacts {
		if a.Component != "cabundle" {
			continue
		}
		n++
		resp, err := http.Get(a.URL + ".sha256")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s.sha256: %s %v", a.URL, resp.Status, err)
		}
		fields := strings.Fields(string(body))
		if len(fields) != 2 || fields[0] != a.SHA256 || !strings.HasSuffix(a.URL, "/"+fields[1]) {
			t.Errorf("%s: manifest sha256 %s, published %q", a.URL, a.SHA256, body)
		}
	}
	if n == 0 {
		t.Fatal("no cabundle in the manifest")
	}
}

// TestXdebugManifestMatchesRelease checks every pinned xdebug.so against the
// SHA256SUMS published in the same benbowdev/benbow-binaries release.
func TestXdebugManifestMatchesRelease(t *testing.T) {
	m, err := components.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	sums := map[string]map[string]string{} // release dir -> file -> sha256
	n := 0
	for _, a := range m.Artifacts {
		if a.Component != "xdebug" {
			continue
		}
		n++
		dir, file := a.URL[:strings.LastIndex(a.URL, "/")], a.URL[strings.LastIndex(a.URL, "/")+1:]
		if !strings.HasPrefix(dir, "https://github.com/benbowdev/benbow-binaries/releases/download/") {
			t.Errorf("%s: not a benbow-binaries release", a.URL)
		}
		if _, ok := sums[dir]; !ok {
			resp, err := http.Get(dir + "/SHA256SUMS")
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || resp.StatusCode != http.StatusOK {
				t.Fatalf("GET %s/SHA256SUMS: %v %s", dir, err, resp.Status)
			}
			sums[dir] = map[string]string{}
			for line := range strings.SplitSeq(string(body), "\n") {
				if f := strings.Fields(line); len(f) == 2 {
					sums[dir][f[1]] = f[0]
				}
			}
		}
		if got := sums[dir][file]; got != a.SHA256 {
			t.Errorf("%s: manifest %s, release %s", file, a.SHA256, got)
		}
	}
	if n == 0 {
		t.Fatal("no xdebug artifacts")
	}
}
