// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package components

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func sign(t *testing.T, priv ed25519.PrivateKey, m Manifest) ([]byte, []byte) {
	t.Helper()
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return body, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body)))
}

func TestVerifySigned(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	m := Manifest{Serial: 7, Artifacts: []Artifact{{Component: "caddy", Version: "2.12.0", Arch: "arm64",
		URL: "https://example.com/caddy.tar.gz", SHA256: "aa00000000000000000000000000000000000000000000000000000000000000", Files: []string{"caddy"}}}}
	body, sig := sign(t, priv, m)

	got, err := VerifySigned(body, sig, []ed25519.PublicKey{otherPub, pub})
	if err != nil || got.Serial != 7 || len(got.Artifacts) != 1 {
		t.Fatalf("valid = %+v, %v", got, err)
	}
	if _, err := VerifySigned(body, sig, []ed25519.PublicKey{otherPub}); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("untrusted key err = %v", err)
	}
	tampered := append([]byte{}, body...)
	tampered[len(tampered)-3] ^= 1
	if _, err := VerifySigned(tampered, sig, []ed25519.PublicKey{pub}); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("tampered body err = %v", err)
	}
	if _, err := VerifySigned(body, []byte("nope"), []ed25519.PublicKey{pub}); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("bad signature err = %v", err)
	}
	if _, err := VerifySigned(body, sig, nil); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("no keys err = %v", err)
	}
	// Signed but over plain HTTP: refused.
	m.Artifacts[0].URL = "http://example.com/caddy.tar.gz"
	body, sig = sign(t, otherPriv, m)
	if _, err := VerifySigned(body, sig, []ed25519.PublicKey{otherPub}); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("http URL err = %v", err)
	}
}

func TestMerge(t *testing.T) {
	sha := func(c byte) string {
		b := make([]byte, 64)
		for i := range b {
			b[i] = c
		}
		return string(b)
	}
	base := Manifest{Serial: 1, Artifacts: []Artifact{{Component: "caddy", Version: "2.11.7", Arch: "arm64", URL: "https://x/1", SHA256: sha('1')}}}
	newer := Manifest{Serial: 2, Artifacts: []Artifact{
		{Component: "caddy", Version: "2.11.7", Arch: "arm64", URL: "https://x/1", SHA256: sha('1')},
		{Component: "caddy", Version: "2.12.0", Arch: "arm64", URL: "https://x/2", SHA256: sha('2')},
	}}
	got, err := base.Merge(newer)
	if err != nil || got.Serial != 2 || len(got.Artifacts) != 2 {
		t.Fatalf("merge = %+v, %v", got, err)
	}
	if v := got.Versions("caddy", "arm64"); len(v) != 2 || v[1] != "2.12.0" {
		t.Fatalf("versions = %v", v)
	}
	// An older manifest that drops a version does not remove it.
	if got, _ := got.Merge(Manifest{Serial: 3}); len(got.Artifacts) != 2 {
		t.Fatalf("dropped artifacts: %+v", got)
	}
	swapped := Manifest{Serial: 3, Artifacts: []Artifact{{Component: "caddy", Version: "2.11.7", Arch: "arm64", URL: "https://x/1", SHA256: sha('9')}}}
	if _, err := base.Merge(swapped); errcode.Code(err) != errcode.Checksum {
		t.Fatalf("swapped binary err = %v", err)
	}
}

func TestParseKeys(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	if keys, err := ParseKeys([]string{base64.StdEncoding.EncodeToString(pub)}); err != nil || len(keys) != 1 {
		t.Fatalf("ParseKeys = %v, %v", keys, err)
	}
	if _, err := ParseKeys([]string{"short"}); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("bad key err = %v", err)
	}
	if _, err := ParseKeys(TrustedKeys); err != nil {
		t.Fatalf("embedded keys: %v", err)
	}
}
