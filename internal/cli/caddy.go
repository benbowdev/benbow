// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

type caddyInstallResult core.CaddyInstall

func (r caddyInstallResult) Text(w io.Writer) {
	verb := "Installed"
	if !r.Fresh {
		verb = "Already installed:"
	}
	fmt.Fprintf(w, "%s Caddy %s\n  %s\n", verb, r.Version, r.Binary)
}

// caddyCmd is hidden: Caddy is installed automatically when needed. It exists
// for debugging and for verifying the download step on its own.
func (a *app) caddyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "caddy",
		Short:  "Manage the bundled Caddy web server",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "install",
		Short: "Download, verify and install Caddy",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.InstallCaddy(ctx)
			return caddyInstallResult(res), err
		}),
	})
	return cmd
}
