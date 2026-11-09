// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package hostshelper

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/benbowdev/benbow/internal/hosts"
)

const original = "127.0.0.1\tlocalhost\n::1\tlocalhost\n127.0.0.1\tstudio.local\t# MAMP PRO - Do NOT remove this entry!\n"

type fixture struct {
	mu        sync.Mutex
	pfCalls   []string
	hostsPath string
	client    Client
	done      chan error
	selfFiles []string
}

// start runs a helper as the current user on temporary files.
func start(t *testing.T, allowed uint32) *fixture {
	t.Helper()
	// A short directory: Unix socket paths are limited to 104 bytes.
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fixture{hostsPath: filepath.Join(dir, "hosts"), done: make(chan error, 1)}
	if err := os.WriteFile(f.hostsPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"bin", "plist"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
		f.selfFiles = append(f.selfFiles, p)
	}
	sock := filepath.Join(dir, "s.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Config: Config{HostsPath: f.hostsPath, AllowedUID: allowed, SelfFiles: f.selfFiles, PF: f.pfctl}}
	go func() { f.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-f.done; err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrUninstalled) {
			t.Errorf("Serve: %v", err)
		}
	})
	f.client = Client{Socket: sock}
	return f
}

// pfctl records calls like "-a com.apple/dev.benbow.hostshelper -f - <rules>".
func (f *fixture) pfctl(args []string, stdin string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pfCalls = append(f.pfCalls, strings.TrimSpace(strings.Join(args, " ")+" "+stdin))
	if slices.Equal(args, []string{"-E"}) {
		return "No ALTQ support in kernel\npf enabled\nToken : 1234567\n", nil
	}
	return "", nil
}

func (f *fixture) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.pfCalls
	f.pfCalls = nil
	return out
}

func TestPorts(t *testing.T) {
	f := start(t, me())
	ctx := context.Background()
	fw := []Forward{{From: 443, To: 8443}, {From: 80, To: 8080}}
	res, err := f.client.Do(ctx, Request{Op: OpPorts, Forwards: fw})
	if err != nil || !res.Changed || len(res.Forwards) != 2 || res.Forwards[0].From != 80 {
		t.Fatalf("ports = %+v, %v", res, err)
	}
	want := []string{
		"-a " + Anchor + " -f - " + strings.TrimSpace(Rules([]Forward{{80, 8080}, {443, 8443}})),
		"-E",
	}
	if got := f.calls(); !slices.Equal(got, want) {
		t.Fatalf("pfctl calls = %q\nwant %q", got, want)
	}
	if !strings.Contains(Rules(fw), "rdr pass on lo0 inet6 proto tcp from any to ::1 port 443 -> ::1 port 8443") {
		t.Fatalf("rules = %s", Rules(fw))
	}
	// The same forwards again: nothing to do. Status reports them.
	if res, err := f.client.Do(ctx, Request{Op: OpPorts, Forwards: fw}); err != nil || res.Changed {
		t.Fatalf("same ports = %+v, %v", res, err)
	}
	if res, err := f.client.Do(ctx, Request{Op: OpStatus}); err != nil || len(res.Forwards) != 2 {
		t.Fatalf("status = %+v, %v", res, err)
	}
	if got := f.calls(); len(got) != 0 {
		t.Fatalf("unexpected pfctl calls %q", got)
	}

	for _, bad := range [][]Forward{
		{{From: 443, To: 80}},
		{{From: 8443, To: 9443}},
		{{From: 0, To: 8443}},
		{{From: 443, To: 70000}},
		{{From: 443, To: 8443}, {From: 443, To: 9443}},
		{{From: 443, To: 8443}, {From: 80, To: 8080}, {From: 22, To: 2222}},
		{{From: 22, To: 2222}},
		{{From: 631, To: 8631}},
	} {
		if _, err := f.client.Do(ctx, Request{Op: OpPorts, Forwards: bad}); err == nil {
			t.Errorf("forwards %+v accepted", bad)
		}
	}
	if got := f.calls(); len(got) != 0 {
		t.Fatalf("pfctl ran for refused forwards: %q", got)
	}

	// No forwards: flush the anchor and release pf.
	if _, err := f.client.Do(ctx, Request{Op: OpPorts}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); !slices.Equal(got, []string{"-a " + Anchor + " -F all", "-X 1234567"}) {
		t.Fatalf("pfctl calls = %q", got)
	}

	// Uninstall removes forwards too.
	if _, err := f.client.Do(ctx, Request{Op: OpPorts, Forwards: fw[:1]}); err != nil {
		t.Fatal(err)
	}
	f.calls()
	if _, err := f.client.Do(ctx, Request{Op: OpUninstall}); err != nil {
		t.Fatal(err)
	}
	if got := f.calls(); !slices.Equal(got, []string{"-a " + Anchor + " -F all", "-X 1234567"}) {
		t.Fatalf("pfctl calls on uninstall = %q", got)
	}
}

func (f *fixture) hosts(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.hostsPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func me() uint32 { return uint32(os.Getuid()) }

func TestSyncStatusClear(t *testing.T) {
	f := start(t, me())
	ctx := context.Background()

	res, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"shop.local", "demo.benbow.local"}})
	if err != nil || !res.Changed || res.Version != Version {
		t.Fatalf("sync: %+v %v", res, err)
	}
	got := f.hosts(t)
	if !strings.HasPrefix(got, original) || !strings.Contains(got, "127.0.0.1\tshop.local\n::1\t\tshop.local\n") {
		t.Fatalf("hosts after sync:\n%s", got)
	}
	info, _ := os.Stat(f.hostsPath)
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", info.Mode().Perm())
	}

	res, err = f.client.Do(ctx, Request{Op: OpStatus})
	if err != nil || !slices.Equal(res.Names, []string{"demo.benbow.local", "shop.local"}) {
		t.Fatalf("status: %+v %v", res, err)
	}
	if res, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"demo.benbow.local", "shop.local"}}); err != nil || res.Changed {
		t.Fatalf("same sync must not write: %+v %v", res, err)
	}

	if _, err := f.client.Do(ctx, Request{Op: OpClear}); err != nil {
		t.Fatal(err)
	}
	if got := f.hosts(t); got != original {
		t.Fatalf("clear did not restore the original:\n%q", got)
	}
	entries, _ := os.ReadDir(filepath.Dir(f.hostsPath))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".benbow-") {
			t.Fatalf("temporary file left: %s", e.Name())
		}
	}
}

func TestRefusesOtherUsers(t *testing.T) {
	f := start(t, me()+1)
	_, err := f.client.Do(context.Background(), Request{Op: OpSync, Names: []string{"a.local"}})
	if err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("err = %v", err)
	}
	if f.hosts(t) != original {
		t.Fatal("hosts changed for a forbidden caller")
	}
}

func TestRefusesBadRequests(t *testing.T) {
	f := start(t, me())
	ctx := context.Background()
	for name, req := range map[string]Request{
		"public name":     {Op: OpSync, Names: []string{"example.com"}},
		"injection":       {Op: OpSync, Names: []string{"a.local\n1.2.3.4 bank.com"}},
		"localhost":       {Op: OpSync, Names: []string{"a.localhost"}},
		"unknown op":      {Op: "write", Names: []string{"a.local"}},
		"names on clear":  {Op: OpClear, Names: []string{"a.local"}},
		"names on status": {Op: OpStatus, Names: []string{"a.local"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := f.client.Do(ctx, req); err == nil {
				t.Fatal("accepted")
			}
			if f.hosts(t) != original {
				t.Fatal("hosts changed")
			}
		})
	}

	// Malformed markers: nothing is written.
	broken := original + hosts.Begin + "\n"
	if err := os.WriteFile(f.hostsPath, []byte(broken), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"a.local"}}); err == nil {
		t.Fatal("sync accepted malformed markers")
	}
	if f.hosts(t) != broken {
		t.Fatal("hosts changed")
	}

	// A symlink in place of the hosts file is refused.
	if err := os.Remove(f.hostsPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(f.hostsPath), "elsewhere")
	if err := os.WriteFile(target, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, f.hostsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"a.local"}}); err == nil {
		t.Fatal("sync followed a symlink")
	}
}

func TestOversizedRequest(t *testing.T) {
	f := start(t, me())
	conn, err := net.Dial("unix", f.client.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, _ = conn.Write([]byte(`{"op":"sync","names":["` + strings.Repeat("a", maxRequest) + `.local"]}`))
	var res Response
	if err := json.NewDecoder(conn).Decode(&res); err != nil || res.OK {
		t.Fatalf("oversized request: %+v %v", res, err)
	}
	if f.hosts(t) != original {
		t.Fatal("hosts changed")
	}
}

func TestUninstall(t *testing.T) {
	f := start(t, me())
	ctx := context.Background()
	if _, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"a.local"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.client.Do(ctx, Request{Op: OpUninstall}); err != nil {
		t.Fatal(err)
	}
	if err := <-f.done; !errors.Is(err, ErrUninstalled) {
		t.Fatalf("Serve returned %v", err)
	}
	f.done <- nil // for the cleanup
	if f.hosts(t) != original {
		t.Fatal("uninstall did not clear the block")
	}
	for _, p := range f.selfFiles {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("%s not removed", p)
		}
	}
}
