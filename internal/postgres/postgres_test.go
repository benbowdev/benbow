// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package postgres

import (
	"os"
	"testing"
)

func TestQuoting(t *testing.T) {
	for in, want := range map[string]string{`a"b`: `"a""b"`, "shop": `"shop"`} {
		if got := Ident(in); got != want {
			t.Errorf("Ident(%q) = %s", in, got)
		}
	}
	if got := Literal(`it's`); got != `'it''s'` {
		t.Errorf("Literal = %s", got)
	}
	if got := passEscape(`a:b\c`); got != `a\:b\\c` {
		t.Errorf("passEscape = %s", got)
	}
}

func TestSecretFile(t *testing.T) {
	path, remove, err := secretFile(t.TempDir(), "secret")
	if err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, %v", fi.Mode(), err)
	}
	remove()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("not removed")
	}
}
