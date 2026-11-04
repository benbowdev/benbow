// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package components

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// TrustedKeys are the public keys (base64, ed25519) whose signatures Covo
// accepts on a published manifest. The private key only exists as a secret
// of getcovo/covo-binaries' GitHub Actions.
var TrustedKeys = []string{
	"o9ILY4BHSpZIeAppoLZPL9zXQ+uabEHfPbeqfqz03EI=", // 2026-11-03
}

// ParseKeys decodes base64 ed25519 public keys.
func ParseKeys(encoded []string) ([]ed25519.PublicKey, error) {
	var out []ed25519.PublicKey
	for _, e := range encoded {
		b, err := base64.StdEncoding.DecodeString(strings.TrimSpace(e))
		if err != nil || len(b) != ed25519.PublicKeySize {
			return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("bad manifest public key %q", e))
		}
		out = append(out, ed25519.PublicKey(b))
	}
	return out, nil
}

// pathPartRe is a component, version or architecture name: one folder
// name, never "." or "..".
var pathPartRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// VerifySigned checks that sig (base64) is a valid signature of body by one
// of keys, then parses body. Nothing in body is trusted before that.
func VerifySigned(body, sig []byte, keys []ed25519.PublicKey) (Manifest, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sig)))
	if err != nil || len(raw) != ed25519.SignatureSize {
		return Manifest{}, errcode.Errorf(errcode.Checksum, "the manifest signature is malformed")
	}
	ok := false
	for _, k := range keys {
		if ed25519.Verify(k, body, raw) {
			ok = true
			break
		}
	}
	if !ok {
		return Manifest{}, errcode.Errorf(errcode.Checksum, "the manifest signature does not match any trusted key")
	}
	var m Manifest
	if err := json.Unmarshal(body, &m); err != nil {
		return Manifest{}, errcode.New(errcode.Checksum, fmt.Errorf("parse signed manifest: %w", err))
	}
	for _, a := range m.Artifacts {
		if a.Component == "" || a.Version == "" || a.URL == "" || len(a.SHA256) != 64 || len(a.Files) == 0 {
			return Manifest{}, errcode.Errorf(errcode.Checksum, fmt.Sprintf("signed manifest has an incomplete %s %s entry", a.Component, a.Version))
		}
		// They become folder names under bin/: never a path.
		for _, part := range []string{a.Component, a.Version, a.For, a.Arch} {
			if part != "" && !pathPartRe.MatchString(part) {
				return Manifest{}, errcode.Errorf(errcode.Checksum, fmt.Sprintf("signed manifest: %q is not a valid name", part))
			}
		}
		if !strings.HasPrefix(a.URL, "https://") {
			return Manifest{}, errcode.Errorf(errcode.Checksum, fmt.Sprintf("signed manifest: %s %s is not served over HTTPS", a.Component, a.Version))
		}
	}
	return m, nil
}

func artifactKey(a Artifact) string {
	return a.Component + "\x00" + a.Version + "\x00" + a.Arch + "\x00" + a.For
}

// Merge returns m with the artifacts of newer that m lacks, and newer's
// serial. Artifacts are never replaced: an entry with the same component,
// version, architecture and target but different content is an error, so
// a published manifest can add versions but never swap a binary Covo
// already trusts.
func (m Manifest) Merge(newer Manifest) (Manifest, error) {
	out := Manifest{Serial: max(m.Serial, newer.Serial), Artifacts: append([]Artifact(nil), m.Artifacts...)}
	have := map[string]Artifact{}
	for _, a := range m.Artifacts {
		have[artifactKey(a)] = a
	}
	for _, a := range newer.Artifacts {
		old, ok := have[artifactKey(a)]
		if !ok {
			out.Artifacts = append(out.Artifacts, a)
			have[artifactKey(a)] = a
			continue
		}
		if old.SHA256 != a.SHA256 || old.URL != a.URL {
			return Manifest{}, errcode.Errorf(errcode.Checksum,
				fmt.Sprintf("signed manifest changes %s %s (%s): artifacts are never replaced", a.Component, a.Version, a.Arch))
		}
	}
	return out, nil
}
