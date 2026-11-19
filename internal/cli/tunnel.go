// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/spf13/cobra"
)

type tunnelResult struct{ *core.TunnelStatus }

func (r tunnelResult) Text(w io.Writer) {
	if r.TunnelStatus == nil {
		fmt.Fprintln(w, "No site has a public link. Open one with: benbow tunnel <site>")
		return
	}
	switch {
	case r.URL == "":
		fmt.Fprintf(w, "%s: waiting for Cloudflare to give the link an address. Check again with: benbow tunnel\n", r.Site)
	case !r.Ready:
		fmt.Fprintf(w, "%s will be on the internet at %s in a few seconds. Check again with: benbow tunnel\n", r.Site, r.URL)
	default:
		fmt.Fprintf(w, "%s is on the internet for %d more minutes:\n  %s\n", r.Site, r.Minutes, r.URL)
	}
	switch {
	case r.Password != "":
		fmt.Fprintf(w, "Visitors need user %s and password %s (shown only now).\n", r.User, r.Password)
	case r.Protected:
		fmt.Fprintln(w, "Visitors need the password shown when the link was opened.")
	default:
		fmt.Fprintln(w, "Anyone with the link can open the site.")
	}
	fmt.Fprintln(w, "Close it with: benbow tunnel --off")
}

func (a *app) tunnelCmd() *cobra.Command {
	var minutes int
	var off, password bool
	cmd := &cobra.Command{
		Use:   "tunnel [site]",
		Short: "Give one site a public link for webhooks or a client (turns itself off)",
		Long: "Publishes one site on the internet at https://<random>.trycloudflare.com through\n" +
			"a Cloudflare quick tunnel (no account), for --minutes (default 60), using\n" +
			"Benbow's own cloudflared. Anyone with the link can open the site unless\n" +
			"--password is given. The address changes every time a link is opened; asking\n" +
			"again for the same site only changes the time. Without a site, shows the\n" +
			"link. The site's files and database are not changed.",
		Args: cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if off {
				if err := c.CloseTunnel(ctx); err != nil {
					return nil, err
				}
				return tunnelResult{}, nil
			}
			if len(args) == 0 {
				st, err := c.Tunnel(ctx)
				return tunnelResult{st}, err
			}
			st, err := c.OpenTunnel(ctx, args[0], minutes, password)
			return tunnelResult{&st}, err
		}),
	}
	cmd.Flags().IntVar(&minutes, "minutes", 60, "close the link after this many minutes (max 480)")
	cmd.Flags().BoolVar(&password, "password", false, "ask visitors for a password (webhooks cannot send one)")
	cmd.Flags().BoolVar(&off, "off", false, "close the link")
	return cmd
}
