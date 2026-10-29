// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "a dir", "f.conf")
	tests := []struct {
		body        string
		wantChanged bool
	}{
		{"one", true},
		{"one", false},
		{"two", true},
	}
	for _, tt := range tests {
		changed, err := WriteAtomic(path, []byte(tt.body), 0o600)
		if err != nil {
			t.Fatal(err)
		}
		if changed != tt.wantChanged {
			t.Fatalf("write %q: changed = %v, want %v", tt.body, changed, tt.wantChanged)
		}
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, err = %v", fi.Mode().Perm(), err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Fatalf("temp files left behind: %v", entries)
	}
}
