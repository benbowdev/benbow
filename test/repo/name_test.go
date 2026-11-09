// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package repo

import (
	"os/exec"
	"strings"
	"testing"
)

// oldNameAllowed are the only tracked files that may say the product's
// former name: the migration from it and the decision that renamed it.
var oldNameAllowed = []string{
	"docs/decisions.md",
	"docs/migrate-from-covo.md",
	"internal/core/migratecovo",
	"internal/cli/migrate",
	"test/e2e/migrate_covo",
	"test/repo/name_test.go",
}

// TestNoFormerName keeps the old name (Covo, getcovo) out of the code,
// docs and app after the rename to Benbow.
func TestNoFormerName(t *testing.T) {
	out, err := exec.Command("git", "-C", root, "grep", "-Iil", "covo").Output()
	if err != nil && len(out) == 0 {
		return // git grep exits 1 when nothing matches
	}
	for _, f := range strings.Fields(string(out)) {
		ok := false
		for _, a := range oldNameAllowed {
			if strings.HasPrefix(f, a) {
				ok = true
			}
		}
		if !ok {
			t.Errorf("%s mentions the former name", f)
		}
	}
	out, _ = exec.Command("git", "-C", root, "ls-files").Output()
	for _, f := range strings.Fields(string(out)) {
		if strings.Contains(strings.ToLower(f), "covo") && !strings.HasPrefix(f, "internal/core/migratecovo") && !strings.HasPrefix(f, "test/e2e/migrate_covo") && f != "docs/migrate-from-covo.md" {
			t.Errorf("file name %s has the former name", f)
		}
	}
}
