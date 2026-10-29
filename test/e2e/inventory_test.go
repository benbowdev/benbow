// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestMain takes an inventory of everything outside the test homes that Covo
// could touch, and fails the run if the tests leave any of it changed.
func TestMain(m *testing.M) {
	before, err := inventory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "inventory before tests:", err)
		os.Exit(1)
	}
	code := m.Run()
	after, err := inventory()
	if err != nil {
		fmt.Fprintln(os.Stderr, "inventory after tests:", err)
		os.Exit(1)
	}
	if diff := diffInventory(before, after); diff != "" {
		fmt.Fprintln(os.Stderr, "the tests changed the Mac outside their homes:\n"+diff)
		code = 1
	}
	os.Exit(code)
}

// inventory returns one entry per item, keyed by what it describes.
func inventory() (map[string]string, error) {
	inv := map[string]string{}

	// Trust settings: the user domain and the admin domain. "No Trust
	// Settings were found" exits non-zero; that is a valid, empty state.
	for _, domain := range []string{"user", "admin"} {
		args := []string{"dump-trust-settings"}
		if domain == "admin" {
			args = append(args, "-d")
		}
		out, _ := exec.Command("/usr/bin/security", args...).CombinedOutput()
		inv["trust-settings "+domain] = string(out)
	}

	// Covo's lines in /etc/hosts: the marked block and any line naming Covo.
	hosts, err := os.ReadFile("/etc/hosts")
	if err != nil {
		return nil, err
	}
	var covo []string
	for line := range strings.SplitSeq(string(hosts), "\n") {
		if strings.Contains(strings.ToLower(line), "covo") {
			covo = append(covo, line)
		}
	}
	inv["/etc/hosts covo lines"] = strings.Join(covo, "\n")

	// Resolver files, with their contents.
	if err := addDir(inv, "/etc/resolver"); err != nil {
		return nil, err
	}

	// Privileged helper and daemons.
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	for _, dir := range []string{
		"/Library/LaunchDaemons", "/Library/PrivilegedHelperTools", "/Library/LaunchAgents",
		filepath.Join(home, "Library", "LaunchAgents"),
	} {
		matches, _ := filepath.Glob(filepath.Join(dir, "dev.getcovo*"))
		inv[dir+" covo"] = strings.Join(matches, "\n")
	}

	// Keychain items Covo creates (MySQL password) and certificates named
	// Covo, by attribute only: no secret is read. Also the keychain search
	// list.
	kc := filepath.Join(home, "Library", "Keychains", "login.keychain-db")
	out, err := exec.Command("/usr/bin/security", "dump-keychain", kc).Output()
	if err != nil {
		return nil, fmt.Errorf("security dump-keychain: %w", err)
	}
	var items []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if l := strings.ToLower(line); strings.Contains(l, "dev.getcovo") || strings.Contains(l, "covo") {
			items = append(items, strings.TrimSpace(line))
		}
	}
	slices.Sort(items)
	inv["login keychain covo items"] = strings.Join(items, "\n")
	out, err = exec.Command("/usr/bin/security", "list-keychains", "-d", "user").Output()
	if err != nil {
		return nil, fmt.Errorf("security list-keychains: %w", err)
	}
	inv["keychain search list"] = string(out)

	// Loaded launchd agents.
	out, err = exec.Command("/bin/launchctl", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("launchctl list: %w", err)
	}
	var labels []string
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && strings.HasPrefix(fields[2], "dev.getcovo") {
			labels = append(labels, fields[2])
		}
	}
	slices.Sort(labels)
	inv["launchd agents"] = strings.Join(labels, "\n")

	return inv, nil
}

func addDir(inv map[string]string, dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		inv[dir] = "(absent)"
		return nil
	}
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		inv[filepath.Join(dir, e.Name())] = string(body)
	}
	inv[dir] = strings.Join(names, "\n")
	return nil
}

func diffInventory(before, after map[string]string) string {
	keys := map[string]bool{}
	for k := range before {
		keys[k] = true
	}
	for k := range after {
		keys[k] = true
	}
	var sorted []string
	for k := range keys {
		sorted = append(sorted, k)
	}
	slices.Sort(sorted)
	var b strings.Builder
	for _, k := range sorted {
		if before[k] != after[k] {
			fmt.Fprintf(&b, "--- %s before:\n%s\n+++ %s after:\n%s\n", k, before[k], k, after[k])
		}
	}
	return b.String()
}

func TestInventoryDetectsChanges(t *testing.T) {
	a := map[string]string{"x": "1", "y": "2"}
	if d := diffInventory(a, map[string]string{"x": "1", "y": "2"}); d != "" {
		t.Fatalf("unexpected diff: %s", d)
	}
	if d := diffInventory(a, map[string]string{"x": "1", "y": "3", "z": "4"}); !strings.Contains(d, "y before") || !strings.Contains(d, "z after") {
		t.Fatalf("diff missed changes: %s", d)
	}
}
