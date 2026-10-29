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
	"testing"

	"github.com/getcovo/covo/internal/hosts"
)

const original = "127.0.0.1\tlocalhost\n::1\tlocalhost\n127.0.0.1\tstudio.local\t# MAMP PRO - Do NOT remove this entry!\n"

type fixture struct {
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
	srv := &Server{Config: Config{HostsPath: f.hostsPath, AllowedUID: allowed, SelfFiles: f.selfFiles}}
	go func() { f.done <- srv.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		if err := <-f.done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("Serve: %v", err)
		}
	})
	f.client = Client{Socket: sock}
	return f
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

	res, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"shop.local", "demo.covo.local"}})
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
	if err != nil || !slices.Equal(res.Names, []string{"demo.covo.local", "shop.local"}) {
		t.Fatalf("status: %+v %v", res, err)
	}
	if res, err := f.client.Do(ctx, Request{Op: OpSync, Names: []string{"demo.covo.local", "shop.local"}}); err != nil || res.Changed {
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
		if strings.Contains(e.Name(), ".covo-") {
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
