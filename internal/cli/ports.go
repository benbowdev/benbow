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

type portsResult core.PortsStatus

func (r portsResult) Text(w io.Writer) {
	https := fmt.Sprintf("%d", r.HTTPS)
	if r.HTTPS != r.HTTPSListen {
		https += fmt.Sprintf(" (forwarded to %d)", r.HTTPSListen)
	}
	fmt.Fprintf(w, "HTTPS  %s\n", https)
	switch {
	case r.HTTP == 0:
		fmt.Fprintln(w, "HTTP   off")
	case r.HTTP != r.HTTPListen:
		fmt.Fprintf(w, "HTTP   %d (forwarded to %d), redirects to HTTPS\n", r.HTTP, r.HTTPListen)
	default:
		fmt.Fprintf(w, "HTTP   %d, redirects to HTTPS\n", r.HTTP)
	}
	if r.Forwarded && len(r.Active) == 0 {
		fmt.Fprintln(w, "Forwards are put in place while Covo is running.")
	}
	if r.Error != "" {
		fmt.Fprintf(w, "Helper: %s\n", r.Error)
	}
}

func (a *app) portsCmd() *cobra.Command {
	var https, http int
	var cmd *cobra.Command
	cmd = &cobra.Command{
		Use:   "ports",
		Short: "Show or change the web ports (--https 443 for addresses without a port)",
		Long: "The HTTPS port is the one in site addresses; 443 leaves it out. The HTTP port\n" +
			"(0: off) only redirects to HTTPS. 443 and 80 need Covo's helper, installed\n" +
			"with one administrator password: it forwards them to Covo while Covo runs.",
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			if !cmd.Flags().Changed("https") && !cmd.Flags().Changed("http") {
				res, err := c.GetPorts(ctx)
				return portsResult(res), err
			}
			cur, err := c.GetPorts(ctx)
			if err != nil {
				return nil, err
			}
			if !cmd.Flags().Changed("https") {
				https = cur.HTTPS
			}
			if !cmd.Flags().Changed("http") {
				http = cur.HTTP
			}
			res, err := c.SetPorts(ctx, https, http, func() {
				if !a.json {
					fmt.Fprintln(a.stderr, "macOS will now ask for an administrator password to install Covo's helper, which forwards the standard ports to Covo.")
				}
			})
			return portsResult(res), err
		}),
	}
	cmd.Flags().IntVar(&https, "https", 0, "HTTPS port: 443, or 1024-65535")
	cmd.Flags().IntVar(&http, "http", 0, "HTTP redirect port: 0 (off), 80, or 1024-65535")
	return cmd
}
