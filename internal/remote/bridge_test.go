// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package remote

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
)

func TestBridgeURL(t *testing.T) {
	tests := []struct {
		url string
		ok  bool
	}{
		{"https://shop.example.com", true},
		{"https://shop.example.com/sub/", true},
		{"http://shop.localhost:8080", true},
		{"http://127.0.0.1:8080", true},
		{"http://[::1]:8080", true},
		{"http://shop.example.com", false},
		{"http://192.168.1.10", false},
		{"ftp://shop.example.com", false},
		{"shop.example.com", false},
	}
	for _, tt := range tests {
		_, _, err := NewBridge(tt.url)
		if (err == nil) != tt.ok {
			t.Errorf("NewBridge(%q) = %v, want ok %v", tt.url, err, tt.ok)
		}
		if err != nil && errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("NewBridge(%q) code = %s", tt.url, errcode.Code(err))
		}
	}
}

// A redirect is reported, never followed: the token stays with the server
// it was meant for.
func TestBridgeDoesNotFollowRedirects(t *testing.T) {
	leaked := make(chan string, 1)
	elsewhere := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		leaked <- r.Header.Get("X-Benbow-Token")
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/benbow-bridge.php", http.StatusMovedPermanently)
	}))
	defer redirecting.Close()

	b := Bridge{URL: redirecting.URL + "/benbow-bridge.php", Token: "secret-token"}
	_, err := b.request(context.Background(), http.MethodGet, "info", nil)
	if err == nil || !strings.Contains(err.Error(), "redirects") {
		t.Fatalf("request = %v, want a redirect error", err)
	}
	select {
	case tok := <-leaked:
		t.Fatalf("the redirect was followed with token %q", tok)
	default:
	}
}

// The target always follows "--", so ssh never reads it as an option.
func TestSSHTargetAfterDoubleDash(t *testing.T) {
	dir := t.TempDir()
	log := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "ssh")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+log+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvSSH, fake)
	s := Server{Transport: SSH, Host: "example.com", User: "deploy"}
	if err := s.Run(context.Background(), "true", nil, io.Discard); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(log)
	args := strings.Split(strings.TrimSpace(string(b)), "\n")
	i := slices.Index(args, "--")
	if i < 0 || i+1 >= len(args) || args[i+1] != "deploy@example.com" {
		t.Fatalf("ssh args = %q", args)
	}
}
