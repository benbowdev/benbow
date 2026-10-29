// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package caddy

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

var update = flag.Bool("update", false, "rewrite golden files")

const home = "/Users/test/Library/Application Support/covo"

var testCA = CA{
	Name:             "Covo Local CA",
	RootCert:         home + "/data/ca/root.crt",
	IntermediateCert: home + "/data/ca/intermediate.crt",
	IntermediateKey:  home + "/data/ca/intermediate.key",
}

func TestCaddyfileGolden(t *testing.T) {
	tests := []struct {
		name  string
		sites []Site
	}{
		{"no-sites", nil},
		{"two-sites", []Site{
			{Name: "demo", Domain: "demo.localhost", Root: "/Users/test/Sites/My Demo", FPMSocket: home + "/run/php84.sock"},
			{Name: "legacy", Domain: "legacy.localhost", Root: "/Users/test/Sites/legacy", FPMSocket: home + "/run/php82.sock"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Caddyfile(Config{
				AdminSocket: home + "/run/caddy-admin.sock",
				HTTPSPort:   8443,
				StorageDir:  home + "/data/caddy",
				LogFile:     home + "/logs/caddy.log",
				CA:          testCA,
				Sites:       tt.sites,
			})
			if err != nil {
				t.Fatal(err)
			}
			golden(t, filepath.Join("testdata", tt.name+".Caddyfile"), got)
		})
	}
}

func TestCaddyfileRejectsUnsafePaths(t *testing.T) {
	_, err := Caddyfile(Config{
		AdminSocket: home + "/run/caddy-admin.sock",
		StorageDir:  home + "/data/caddy",
		LogFile:     home + "/logs/caddy.log",
		CA:          testCA,
		Sites:       []Site{{Name: "x", Domain: "x.localhost", Root: "/Users/test/{env.HOME}", FPMSocket: home + "/run/php84.sock"}},
	})
	if errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("err = %v, want invalid_input", err)
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
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
		t.Errorf("%s mismatch:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}
