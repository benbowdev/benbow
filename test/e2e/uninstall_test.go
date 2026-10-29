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

func (h *harness) home() string {
	h.t.Helper()
	for _, kv := range h.env {
		if v, ok := strings.CutPrefix(kv, "COVO_HOME="); ok {
			return v
		}
	}
	h.t.Fatal("no COVO_HOME")
	return ""
}

func TestUninstallRestoresInitialState(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	h.mustCovo("start")
	if len(h.loadedLabels()) == 0 {
		t.Fatal("nothing running before uninstall")
	}

	// Without --yes and without a terminal, uninstall refuses.
	if env := h.covo("uninstall"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("uninstall without --yes: %+v", env)
	}

	var res struct {
		Done   bool     `json:"done"`
		Agents []string `json:"agents"`
		Paths  []string `json:"paths"`
	}
	if err := json.Unmarshal(h.mustCovo("uninstall", "--yes"), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Done || len(res.Agents) != 2 || len(res.Paths) == 0 {
		t.Fatalf("uninstall result = %+v", res)
	}
	if labels := h.loadedLabels(); len(labels) != 0 {
		t.Fatalf("agents still loaded: %v", labels)
	}
	for _, p := range res.Paths {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Errorf("%s still exists", p)
		}
	}

	// Idempotent: a second run has nothing to do.
	if err := json.Unmarshal(h.mustCovo("uninstall", "--yes"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Agents)+len(res.Paths) != 0 {
		t.Fatalf("second uninstall found %+v", res)
	}
}

func TestUninstallPartialInstall(t *testing.T) {
	h := newHarness(t)
	home := h.home()
	// An install interrupted during a download: only tmp/ and a lock file.
	if err := os.MkdirAll(filepath.Join(home, "tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "tmp", "download-123"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "covo.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("uninstall", "--yes")
	if _, err := os.Lstat(home); !os.IsNotExist(err) {
		t.Fatalf("home still exists: %v", err)
	}
}

func TestUninstallRefusesForeignFiles(t *testing.T) {
	h := newHarness(t)
	home := h.home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(home, "my-notes.txt")
	if err := os.WriteFile(foreign, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}
	if env := h.covo("uninstall", "--yes"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("uninstall with foreign files: %+v", env)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file was touched: %v", err)
	}
	_ = os.Remove(foreign) // let the harness cleanup succeed
}
