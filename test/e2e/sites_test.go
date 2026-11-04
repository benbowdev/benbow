// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// TestSiteLifecycleWhileRunning adds and removes sites with Covo already
// running: changes must go live without a manual restart.
func TestSiteLifecycleWhileRunning(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("start")
	caddyPID := h.status().Services[0].PID

	var added struct {
		URL     string `json:"url"`
		Applied bool   `json:"applied"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "add", "live", "--php", "8.4", "--path", newSite(t)), &added); err != nil {
		t.Fatal(err)
	}
	if !added.Applied {
		t.Fatal("site added while running should be applied")
	}
	ca := h.status().CA.CertPath
	if body := h.get(added.URL, ca); !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("body = %q", body)
	}
	if pid := h.status().Services[0].PID; pid != caddyPID {
		t.Fatalf("adding a site restarted caddy (%d -> %d); it should reload", caddyPID, pid)
	}

	assertLoopbackOnly(t, h.port)

	// Duplicates and bad input are rejected with stable codes.
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"site", "add", "live", "--path", newSite(t)}, "conflict"},
		{[]string{"site", "add", "Bad_Name", "--path", newSite(t)}, "invalid_input"},
		{[]string{"site", "add", "x", "--php", "8", "--path", newSite(t)}, "invalid_input"},
		{[]string{"site", "add", "x", "--path", "/does/not/exist"}, "not_found"},
		{[]string{"site", "remove", "nope"}, "not_found"},
	} {
		env := h.covo(tc.args...)
		if env.OK || env.Error == nil || env.Error.Code != tc.code {
			t.Errorf("covo %v: %+v, want code %s", tc.args, env.Error, tc.code)
		}
	}

	// Rename, with the address: served at the new one.
	var renamed struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "rename", "live", "renamed", "--domain"), &renamed); err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "renamed" || !strings.Contains(renamed.URL, "renamed.localhost") {
		t.Fatalf("renamed = %+v", renamed)
	}
	if body := h.get(renamed.URL, ca); !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("renamed body = %q", body)
	}
	if env := h.covo("site", "rename", "renamed", "Bad Name"); env.OK || env.Error.Code != "invalid_input" {
		t.Errorf("bad rename: %+v", env.Error)
	}

	h.mustCovo("site", "remove", "renamed")
	for _, svc := range h.status().Services {
		if svc.Name != "caddy" {
			t.Errorf("service %s still listed after its last site was removed", svc.Name)
		}
	}
	if labels := h.loadedLabels(); slices.ContainsFunc(labels, func(l string) bool { return strings.HasSuffix(l, ".php84") }) {
		t.Errorf("php84 agent still loaded: %v", labels)
	}
}

// assertLoopbackOnly checks that nothing listens on port outside loopback.
func assertLoopbackOnly(t *testing.T, port int) {
	t.Helper()
	out, err := exec.Command("/usr/sbin/lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fn").Output()
	if err != nil {
		t.Fatalf("lsof: %v", err)
	}
	var addrs []string
	for _, line := range strings.Split(string(out), "\n") {
		if a, ok := strings.CutPrefix(line, "n"); ok {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		t.Fatal("nothing listening")
	}
	for _, a := range addrs {
		if !strings.HasPrefix(a, "127.0.0.1:") && !strings.HasPrefix(a, "[::1]:") {
			t.Errorf("listening on non-loopback address %s", a)
		}
	}
}

func TestSiteAddFolderChecks(t *testing.T) {
	h := newHarness(t)
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	empty := t.TempDir()
	for _, tc := range []struct {
		args []string
		code string
	}{
		{[]string{"site", "add", "home", "--path", home}, "forbidden_folder"},
		{[]string{"site", "add", "root", "--path", "/"}, "forbidden_folder"},
		{[]string{"site", "add", "sys", "--path", "/System/Library"}, "forbidden_folder"},
		{[]string{"site", "add", "empty", "--path", empty}, "confirmation_required"},
	} {
		env := h.covo(tc.args...)
		if env.OK || env.Error == nil || env.Error.Code != tc.code {
			t.Errorf("covo %v: %+v, want code %s", tc.args, env.Error, tc.code)
		}
	}
	h.mustCovo("site", "add", "empty", "--path", empty, "--force")
	// --force never bypasses the forbidden folders.
	if env := h.covo("site", "add", "home", "--path", home, "--force"); env.OK || env.Error.Code != "forbidden_folder" {
		t.Errorf("--force on home: %+v", env.Error)
	}
}
