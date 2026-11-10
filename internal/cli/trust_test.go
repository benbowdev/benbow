// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"bytes"
	"testing"

	"github.com/benbowdev/benbow/internal/ca"
	"github.com/benbowdev/benbow/internal/trust"
)

func TestTrustResultUsesCAName(t *testing.T) {
	t.Setenv(ca.EnvName, "Benbow Demo CA")
	tests := []struct {
		name string
		res  trustResult
		want string
	}{
		{"trusted now, name from the certificate", trustResult{Trusted: true, Changed: true, Cert: &trust.Cert{CommonName: "Benbow Cert CA"}}, "Benbow Cert CA is now trusted: https://*.localhost sites open without warnings.\n"},
		{"already trusted, name from the environment", trustResult{Trusted: true}, "Benbow Demo CA is already trusted.\n"},
		{"not in the keychain", trustResult{}, "Benbow Demo CA was not in the login keychain.\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b bytes.Buffer
			tt.res.Text(&b)
			if got := b.String(); got != tt.want {
				t.Errorf("Text() = %q, want %q", got, tt.want)
			}
		})
	}
}
