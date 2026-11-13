// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package fsutil

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// TestDiskUsage checks that a fresh APFS clone is all shared, and that
// rewriting a file makes it private again.
func TestDiskUsage(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 1<<20)
	for i := range body {
		body[i] = byte(i)
	}
	for _, f := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(src, f), body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	u, err := DiskUsage(src)
	if err != nil || u.Files != 2 || u.Apparent < 2<<20 || u.Private != u.Apparent {
		t.Fatalf("src = %+v, %v", u, err)
	}
	clone := filepath.Join(dir, "clone")
	if err := unix.Clonefile(src, clone, unix.CLONE_NOFOLLOW); err != nil {
		t.Skipf("no APFS clones here: %v", err)
	}
	u, err = DiskUsage(clone)
	if err != nil || u.Apparent < 2<<20 || u.Private != 0 {
		t.Fatalf("fresh clone = %+v, %v", u, err)
	}
	if err := os.WriteFile(filepath.Join(src, "a"), body[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	u, err = DiskUsage(clone)
	if err != nil || u.Private < 1<<20 || u.Private >= u.Apparent {
		t.Fatalf("clone after the source changed = %+v, %v", u, err)
	}
}
