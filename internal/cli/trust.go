// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/benbowdev/benbow/internal/ca"
	"github.com/benbowdev/benbow/internal/core"
	"github.com/spf13/cobra"
)

type trustResult core.TrustResult

// name is the CA's name: the certificate's own when known, else the name in
// effect (BENBOW_CA_NAME or the default).
func (r trustResult) name() string {
	if r.Cert != nil && r.Cert.CommonName != "" {
		return r.Cert.CommonName
	}
	return ca.Name()
}

func (r trustResult) Text(w io.Writer) {
	switch {
	case r.Trusted && r.Changed:
		fmt.Fprintf(w, "%s is now trusted: https://*.localhost sites open without warnings.\n", r.name())
	case r.Trusted:
		fmt.Fprintf(w, "%s is already trusted.\n", r.name())
	case r.Changed:
		for _, c := range r.Removed {
			fmt.Fprintf(w, "Removed %s (SHA-1 %s) from the login keychain.\n", c.CommonName, c.SHA1)
		}
	default:
		fmt.Fprintf(w, "%s was not in the login keychain.\n", r.name())
	}
}

func (a *app) trustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "trust",
		Short: "Trust Benbow's local certificate authority (asks macOS once)",
		Long: `Adds Benbow's local root certificate to your login keychain as trusted for
your user only. macOS shows one dialog asking for your password or Touch ID.
No sudo, nothing system-wide. Undo with: benbow untrust`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Trust(ctx, func() {
				fmt.Fprintf(a.stderr, "macOS will now ask for your password or Touch ID to trust %q for your user.\n", ca.Name())
			})
			return trustResult(res), err
		}),
	}
}

func (a *app) untrustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "untrust",
		Short: "Remove Benbow's certificate authority from the login keychain",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Untrust(ctx)
			return trustResult(res), err
		}),
	}
}
