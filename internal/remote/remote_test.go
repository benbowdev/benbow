// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package remote

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSkipper(t *testing.T) {
	skip := Skipper([]string{".git", "node_modules", "*.log", "cache/"})
	for rel, want := range map[string]bool{
		".git": true, "a/node_modules": true, "a/b/debug.log": true, "wp-content/cache": true,
		"wp-content/uploads/2026/x.jpg": false, "cachedir": false,
	} {
		if got := skip(rel); got != want {
			t.Errorf("skip(%s) = %v", rel, got)
		}
	}
}

func TestChanged(t *testing.T) {
	now := time.Now()
	src := Files{"a": {Size: 1, ModTime: now}, "b": {Size: 2, ModTime: now}, "c": {Size: 3, ModTime: now}, "d": {Dir: true}, "e": {Size: 5, ModTime: now}}
	dst := Files{"a": {Size: 1, ModTime: now}, "b": {Size: 9, ModTime: now}, "e": {Size: 5, ModTime: now.Add(-time.Hour)}}
	got := Changed(src, dst)
	slices.Sort(got)
	if strings.Join(got, ",") != "b,c,e" {
		t.Fatalf("changed = %v", got)
	}
}

func TestQuote(t *testing.T) {
	if got := Quote(`/srv/it's here`); got != `'/srv/it'\''s here'` {
		t.Fatalf("Quote = %s", got)
	}
}

func TestBridgeSource(t *testing.T) {
	b, src, err := NewBridge("https://shop.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.File, "covo-bridge-") || len(b.Token) != 64 || b.URL != "https://shop.example.com/"+b.File {
		t.Fatalf("bridge = %+v", b)
	}
	if !strings.Contains(src, "'"+b.Token+"'") || strings.Contains(src, "COVO_TOKEN_VALUE") || strings.Contains(src, "COVO_EXPIRES_VALUE") {
		t.Fatal("token or expiry not filled in")
	}
	if !strings.Contains(src, "hash_equals") || !strings.Contains(src, "unlink(__FILE__)") {
		t.Fatal("bridge lacks its checks")
	}
}
