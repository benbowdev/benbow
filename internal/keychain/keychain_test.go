// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package keychain

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
)

func TestSetGetAnySecret(t *testing.T) {
	ctx := context.Background()
	secrets := []string{
		"Hunter2!",
		"ghp_abcDEF123_xyz",
		"github_pat_11AAAA_bbbb",
		`a "quoted" \back'slash`,
		"with spaces inside",
		" leading and trailing ",
		"àé ü 日本",
		"x\ny",
		"-w injected",
		"short",
	}
	for _, s := range secrets {
		f := NewFake()
		it := Item{Path: "/k", Service: "dev.benbow.remote", Account: "shop/production", Run: f.Run}
		if err := it.Set(ctx, s); err != nil {
			t.Fatalf("Set(%q) = %v", s, err)
		}
		if got, err := it.Get(ctx); err != nil || got != s {
			t.Fatalf("Get after Set(%q) = %q, %v", s, got, err)
		}
		for i, args := range f.Calls {
			if strings.Contains(strings.Join(args, " "), s) {
				t.Fatalf("secret in arguments: %v", args)
			}
			if strings.Contains(f.Stdin[i], s) {
				t.Fatalf("secret in clear on stdin: %q", f.Stdin[i])
			}
		}
	}
}

func TestSetRejects(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	tests := []struct {
		name string
		it   Item
		s    string
	}{
		{"empty secret", Item{Path: "/k", Service: "s", Account: "a"}, ""},
		{"quote in service", Item{Path: "/k", Service: `s" -w x`, Account: "a"}, "pw"},
		{"backslash in account", Item{Path: "/k", Service: "s", Account: `a\`}, "pw"},
		{"newline in path", Item{Path: "/k\nadd", Service: "s", Account: "a"}, "pw"},
		{"no account", Item{Path: "/k", Service: "s"}, "pw"},
	}
	for _, tt := range tests {
		tt.it.Run = f.Run
		if err := tt.it.Set(ctx, tt.s); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("%s: err = %v", tt.name, err)
		}
	}
}

func TestDeleteAndExists(t *testing.T) {
	ctx := context.Background()
	f := NewFake()
	a := Item{Path: "/k", Service: "svc", Account: "a", Run: f.Run}
	b := a
	b.Account = "b"
	if _, err := a.Get(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get on empty = %v", err)
	}
	for _, it := range []Item{a, b} {
		if err := it.Set(ctx, "secret-"+it.Account); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := a.Exists(ctx); !ok || err != nil {
		t.Fatalf("Exists = %v, %v", ok, err)
	}
	if ok, err := a.Delete(ctx); !ok || err != nil {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if ok, err := a.Delete(ctx); ok || err != nil {
		t.Fatalf("second Delete = %v, %v", ok, err)
	}
	if ok, err := a.AnyExists(ctx); !ok || err != nil {
		t.Fatalf("AnyExists = %v, %v", ok, err)
	}
	if n, err := a.DeleteAll(ctx); n != 1 || err != nil {
		t.Fatalf("DeleteAll = %d, %v", n, err)
	}
}

// A lost `security -i` write (it exits 0 on failure) is detected.
func TestSetVerifies(t *testing.T) {
	f := NewFake()
	it := Item{Path: "/k", Service: "s", Account: "a", Run: func(ctx context.Context, stdin string, args ...string) (string, error) {
		if args[0] == "-i" {
			return "", nil
		}
		return f.Run(ctx, stdin, args...)
	}}
	if err := it.Set(context.Background(), "pw"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}

// The real `security -g` output: hex for bytes that are not printable,
// text between quotes otherwise (quotes inside are not escaped).
func TestParseSecret(t *testing.T) {
	tests := []struct{ out, want string }{
		{"keychain: \"/k\"\nclass: \"genp\"\npassword: \"Hunter2!\"\n", "Hunter2!"},
		{"password: \"a\"b\"\n", `a"b`},
		{"password: \" lead\"\n", " lead"},
		{"password: 0x6120227122205C622763  \"a \"q\" \\134b'c\"\n", `a "q" \b'c`},
		{"password: 0xC3A0C3A920C3BC  \"\\303\\240\"\n", "àé ü"},
		{"password: 0x780A79  \"x\\012y\"\n", "x\ny"},
	}
	for _, tt := range tests {
		got, err := parseSecret(tt.out)
		if err != nil || got != tt.want {
			t.Errorf("parseSecret(%q) = %q, %v; want %q", tt.out, got, err, tt.want)
		}
	}
	if _, err := parseSecret("nothing"); err == nil {
		t.Error("parseSecret without a password line succeeded")
	}
}

func TestEntries(t *testing.T) {
	out := "keychain: \"/k\"\nversion: 512\nclass: \"genp\"\nattributes:\n" +
		"    0x00000007 <blob>=\"Old MySQL root (dev.old.mysql)\"\n    \"acct\"<blob>=\"root\"\n    \"svce\"<blob>=\"dev.old.mysql\"\n" +
		"keychain: \"/k\"\nclass: \"genp\"\nattributes:\n    0x00000007 <blob>=<NULL>\n    \"acct\"<blob>=0x73686F70  \"shop\"\n    \"svce\"<blob>=\"dev.old.mysql\"\n" +
		"keychain: \"/k\"\nclass: \"genp\"\nattributes:\n    \"acct\"<blob>=\"x\"\n    \"svce\"<blob>=\"dev.other\"\n" +
		"keychain: \"/k\"\nclass: \"inet\"\nattributes:\n    \"acct\"<blob>=\"y\"\n    \"svce\"<blob>=\"dev.old.mysql\"\n"
	i := Item{Path: "/k", Service: "dev.old.mysql", Run: func(context.Context, string, ...string) (string, error) { return out, nil }}
	got, err := i.Entries(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{"root", "Old MySQL root (dev.old.mysql)"}, {"shop", ""}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("entries = %+v, want %+v", got, want)
	}
}
