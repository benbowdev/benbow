// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package mysql

import (
	"context"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

var update = flag.Bool("update", false, "rewrite golden files")

const home = "/Users/test/Library/Application Support/covo"

func config() Config {
	return Config{
		BaseDir:  home + "/bin/mysql/8.4.13",
		DataDir:  home + "/data/mysql",
		Socket:   home + "/run/mysql.sock",
		PIDFile:  home + "/run/mysql.pid",
		ErrorLog: home + "/logs/mysql-error.log",
		Port:     3307,
	}
}

func TestMyCnfGolden(t *testing.T) {
	got, err := MyCnf(config())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "my.cnf")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if got != string(want) {
		t.Fatalf("mismatch:\n%s", got)
	}
}

func TestMyCnfRejects(t *testing.T) {
	for name, mutate := range map[string]func(*Config){
		"quote":    func(c *Config) { c.DataDir += `"` },
		"hash":     func(c *Config) { c.DataDir += "#x" },
		"apos":     func(c *Config) { c.Socket += "'" },
		"newline":  func(c *Config) { c.ErrorLog += "\nskip-grant-tables" },
		"relative": func(c *Config) { c.BaseDir = "bin/mysql" },
		"port":     func(c *Config) { c.Port = 80 },
	} {
		t.Run(name, func(t *testing.T) {
			c := config()
			mutate(&c)
			if _, err := MyCnf(c); errcode.Code(err) != errcode.InvalidInput {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestNewPassword(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		pw := NewPassword()
		if len(pw) != 24 || checkPassword(pw) != nil || seen[pw] {
			t.Fatalf("bad password %q", pw)
		}
		seen[pw] = true
	}
	for _, bad := range []string{"short", "has space in it 1234567", "quote'quotequotequote", `back\slashback\slash12`} {
		if checkPassword(bad) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// fakeSecurity is an in-memory `security` that records how it was called.
type fakeSecurity struct {
	items map[string]string
	calls [][]string
	stdin []string
}

func (f *fakeSecurity) run(_ context.Context, stdin string, args ...string) (string, error) {
	f.calls = append(f.calls, args)
	f.stdin = append(f.stdin, stdin)
	notFound := errors.New("exit status 44")
	const missing = "security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain."
	flag := func(fields []string, name string) string {
		for i, a := range fields {
			if a == name && i+1 < len(fields) {
				return strings.Trim(fields[i+1], `"`)
			}
		}
		return ""
	}
	if args[0] == "-i" {
		fields := strings.Fields(stdin)
		f.items[flag(fields, "-a")+"@"+flag(fields, "-s")] = flag(fields, "-w")
		return "", nil
	}
	acct, svc := flag(args, "-a"), flag(args, "-s")
	match := ""
	for key := range f.items {
		a, s, _ := strings.Cut(key, "@")
		if s == svc && (acct == "" || a == acct) {
			match = key
			break
		}
	}
	if match == "" {
		return missing, notFound
	}
	switch args[0] {
	case "find-generic-password":
		return f.items[match] + "\n", nil
	case "delete-generic-password":
		delete(f.items, match)
		return "", nil
	}
	return "", errors.New("unexpected")
}

func TestKeychain(t *testing.T) {
	ctx := context.Background()
	f := &fakeSecurity{items: map[string]string{}}
	k := Keychain{Path: "/Users/test/Library/Keychains/login.keychain-db", Service: "dev.getcovo.mysql", Run: f.run}

	if _, err := k.Get(ctx); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("Get on empty = %v", err)
	}
	pw := NewPassword()
	if err := k.Set(ctx, pw); err != nil {
		t.Fatal(err)
	}
	if got, err := k.Get(ctx); err != nil || got != pw {
		t.Fatalf("Get = %q, %v", got, err)
	}
	// The password only ever travels on stdin.
	for _, args := range f.calls {
		if strings.Contains(strings.Join(args, " "), pw) {
			t.Fatalf("password in arguments: %v", args)
		}
	}
	sent := false
	for i, args := range f.calls {
		sent = sent || args[0] == "-i" && strings.Contains(f.stdin[i], " -w "+pw+" ")
	}
	if !sent {
		t.Fatal("password not sent on stdin")
	}
	if ok, err := k.Delete(ctx); !ok || err != nil {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if ok, err := k.Delete(ctx); ok || err != nil {
		t.Fatalf("second Delete = %v, %v", ok, err)
	}
	if err := k.Set(ctx, "bad password with spaces"); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("Set bad = %v", err)
	}
	bad := k
	bad.Service = `x" -w other`
	if err := bad.Set(ctx, pw); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("Set with quote in service = %v", err)
	}
}

// A lost `security -i` write (it exits 0 on failure) is detected.
func TestKeychainAccounts(t *testing.T) {
	ctx := context.Background()
	f := &fakeSecurity{items: map[string]string{}}
	root := Keychain{Path: "/k", Service: "dev.getcovo.mysql", Run: f.run}
	shop := root.For("shop")
	rootPW, shopPW := NewPassword(), NewPassword()
	if err := root.Set(ctx, rootPW); err != nil {
		t.Fatal(err)
	}
	if err := shop.Set(ctx, shopPW); err != nil {
		t.Fatal(err)
	}
	if got, _ := root.Get(ctx); got != rootPW {
		t.Fatalf("root = %q", got)
	}
	if got, _ := shop.Get(ctx); got != shopPW {
		t.Fatalf("shop = %q", got)
	}
	if err := root.For("bad name").Set(ctx, NewPassword()); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("bad account err = %v", err)
	}
	if ok, err := root.AnyExists(ctx); !ok || err != nil {
		t.Fatalf("AnyExists = %v, %v", ok, err)
	}
	if n, err := root.DeleteAll(ctx); n != 2 || err != nil {
		t.Fatalf("DeleteAll = %d, %v", n, err)
	}
	if ok, err := root.AnyExists(ctx); ok || err != nil {
		t.Fatalf("AnyExists after DeleteAll = %v, %v", ok, err)
	}
}

func TestKeychainSetVerifies(t *testing.T) {
	f := &fakeSecurity{items: map[string]string{}}
	k := Keychain{Path: "/k", Service: "s", Run: func(ctx context.Context, stdin string, args ...string) (string, error) {
		if args[0] == "-i" {
			return "", nil // pretend success, store nothing
		}
		return f.run(ctx, stdin, args...)
	}}
	if err := k.Set(context.Background(), NewPassword()); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("err = %v", err)
	}
}
