// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

type phoneResult struct{ *core.PhoneStatus }

func (r phoneResult) Text(w io.Writer) {
	if r.PhoneStatus == nil {
		fmt.Fprintln(w, "No site is open on the phone. Open one with: covo phone <site>")
		return
	}
	if r.PlainURL != "" {
		fmt.Fprintf(w, "%s is open on your local network for %d more minutes:\n  %s\n", r.Site, r.Minutes, r.PlainURL)
		fmt.Fprintf(w, "With HTTPS (install Covo's certificate on the phone first, %s):\n  %s\n", r.CAPageURL, r.URL)
	} else {
		fmt.Fprintf(w, "%s is open on your local network for %d more minutes:\n  %s\n", r.Site, r.Minutes, r.URL)
		fmt.Fprintf(w, "Install Covo's certificate on the phone first: %s\n", r.CAPageURL)
	}
	fmt.Fprintf(w, "Served on %s only. Turn it off with: covo phone --off\n", strings.Join(r.Addresses, ", "))
	if r.Firewall == "block-all" {
		fmt.Fprintln(w, "The macOS firewall blocks all incoming connections: allow them in System Settings → Network → Firewall.")
	}
}

func (a *app) phoneCmd() *cobra.Command {
	var minutes int
	var off bool
	cmd := &cobra.Command{
		Use:   "phone [site]",
		Short: "Open one site on your phone over the local network (turns itself off)",
		Long: "Serves one site at <site>-<mac>.local on this Mac's private network addresses\n" +
			"(Bonjour), with HTTPS from Covo's CA, for --minutes (default 30). Without a\n" +
			"site, shows what is shared. The site's files and database are not changed.",
		Args: cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if off {
				if err := c.StopPhone(ctx); err != nil {
					return nil, err
				}
				return phoneResult{}, nil
			}
			if len(args) == 0 {
				st, err := c.Phone(ctx)
				return phoneResult{st}, err
			}
			st, err := c.SharePhone(ctx, args[0], minutes)
			return phoneResult{&st}, err
		}),
	}
	cmd.Flags().IntVar(&minutes, "minutes", 30, "turn off after this many minutes (max 1440)")
	cmd.Flags().BoolVar(&off, "off", false, "stop sharing")
	return cmd
}
