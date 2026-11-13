// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build benbowtest

package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/components"
	"github.com/benbowdev/benbow/internal/paths"
)

// A manifest stored by another benbow (`benbow update check` from the CLI)
// reaches a running core without a restart; a half-written one (new body,
// old signature) is read again once complete.
func TestStoredManifestIsReloaded(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	t.Setenv("BENBOW_HOME", home)
	t.Setenv(EnvManifestTestKey, base64.StdEncoding.EncodeToString(pub))
	c := &Core{Layout: paths.Layout{Data: filepath.Join(home, "data")}, Manifest: components.Manifest{Serial: 10}}
	dir := c.manifestDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(serial int64, goodSig bool, at time.Time) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{
			"serial": serial, "expires": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"artifacts": []map[string]any{{"component": "mysql57", "version": "5.7.44", "arch": "arm64",
				"url": "https://example.invalid/m.tar.gz", "sha256": strings.Repeat("ab", 32), "files": []string{"bin/"}}},
		})
		sig := ed25519.Sign(priv, body)
		if !goodSig {
			sig = ed25519.Sign(priv, []byte("old"))
		}
		p := filepath.Join(dir, "manifest.json")
		if err := os.WriteFile(p, body, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p+".sig", []byte(base64.StdEncoding.EncodeToString(sig)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, at, at); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Now().Add(-time.Hour)
	write(11, false, t0)
	c.reloadSignedManifest()
	if c.Manifest.Serial != 10 {
		t.Fatalf("a manifest with a bad signature was taken: serial %d", c.Manifest.Serial)
	}
	write(11, true, t0) // same time: the signature arrived after the body
	c.reloadSignedManifest()
	if c.Manifest.Serial != 11 {
		t.Fatalf("serial = %d after the signature was written, want 11", c.Manifest.Serial)
	}
	write(12, true, t0.Add(time.Minute))
	c.reloadSignedManifest()
	if c.Manifest.Serial != 12 {
		t.Fatalf("serial = %d after a newer manifest was stored, want 12", c.Manifest.Serial)
	}
}
