// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTailLines(t *testing.T) {
	dir := t.TempDir()
	if got, size, err := tailLines(filepath.Join(dir, "missing.log"), 5); err != nil || got != nil || size != 0 {
		t.Fatalf("missing = %v %d %v", got, size, err)
	}
	p := filepath.Join(dir, "a.log")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := tailLines(p, 2); strings.Join(got, ",") != "two,three" {
		t.Fatalf("last 2 = %v", got)
	}
	if got, _, _ := tailLines(p, 10); len(got) != 3 {
		t.Fatalf("last 10 = %v", got)
	}
	// Longer than what is read: the first, cut line is dropped.
	big := strings.Repeat("x", maxLogTail) + "\nlast\n"
	if err := os.WriteFile(p, []byte(big), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := tailLines(p, 10); strings.Join(got, ",") != "last" {
		t.Fatalf("big file = %d lines", len(got))
	}
}

func TestFormatAccess(t *testing.T) {
	ts := time.Date(2026, 11, 2, 21, 40, 1, 0, time.Local)
	in := `{"level":"info","ts":` + strconv.FormatInt(ts.Unix(), 10) + `.5,"msg":"handled request",` +
		`"request":{"method":"GET","host":"demo.localhost:8443","uri":"/wp-admin/?a=1"},"duration":0.0123,"status":302}`
	want := "2026-11-02 21:40:01  302  GET  demo.localhost/wp-admin/?a=1  12 ms"
	if got := formatAccess(in); got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if got := formatAccess("not json"); got != "not json" {
		t.Fatalf("raw line changed: %q", got)
	}
}
