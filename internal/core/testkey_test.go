// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build !benbowtest

package core

import (
	"crypto/ed25519"
	"encoding/base64"
	"testing"

	"github.com/benbowdev/benbow/internal/components"
)

// A normal build never trusts a manifest key from the environment.
func TestTrustedKeysIgnoreTheTestKey(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvManifestTestKey, base64.StdEncoding.EncodeToString(pub))
	t.Setenv("BENBOW_HOME", t.TempDir())
	keys, err := trustedKeys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != len(components.TrustedKeys) {
		t.Fatalf("%d keys trusted, want only the %d built in", len(keys), len(components.TrustedKeys))
	}
}
