// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/dns"
	"github.com/spf13/cobra"
)

type domainsResult []core.DomainStatus

func (r domainsResult) Text(w io.Writer) {
	fmt.Fprintln(w, ".localhost  always on")
	for _, d := range r {
		writeDomain(w, d)
	}
}

func writeDomain(w io.Writer, d core.DomainStatus) {
	switch {
	case d.Enabled && d.Resolver.Installed:
		fmt.Fprintf(w, ".%-10s on (DNS 127.0.0.1:%d, %s)\n", d.Zone, d.DNSPort, d.Resolver.Path)
	case d.Enabled:
		fmt.Fprintf(w, ".%-10s on, but %s is missing: run `covo domains enable %s`\n", d.Zone, d.Resolver.Path, d.Zone)
	default:
		fmt.Fprintf(w, ".%-10s off (enable with `covo domains enable %s`)\n", d.Zone, d.Zone)
	}
	if d.Resolver.Foreign {
		fmt.Fprintf(w, "  %s exists and was not created by this Covo installation.\n", d.Resolver.Path)
	}
}

type domainResult core.DomainResult

func (r domainResult) Text(w io.Writer) {
	writeDomain(w, r.DomainStatus)
	if r.Enabled && !r.Applied {
		fmt.Fprintln(w, "Covo is not running: the DNS server starts with `covo start`.")
	}
}

func (a *app) domainsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "domains",
		Short: "Show or change which local domain suffixes Covo serves",
		Long: `Sites are served at <name>.localhost by default: nothing to set up.

.test domains (e.g. demo.covo.test) are opt-in: Covo adds a file to
/etc/resolver that sends .test lookups to its own DNS server on 127.0.0.1.
Enabling and disabling each ask for one administrator authorization.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Domains(ctx)
			return domainsResult(res), err
		}),
	}
	notice := func(action string) func() {
		return func() {
			fmt.Fprintf(a.stderr, "macOS will now ask for an administrator password to %s /etc/resolver/test.\n", action)
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use:       "enable test",
		Short:     "Serve .test domains (one administrator authorization)",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"test"},
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.EnableDomain(ctx, args[0], notice("create"))
			return domainResult(res), err
		}),
	}, &cobra.Command{
		Use:       "disable test",
		Short:     "Stop serving .test domains and remove the resolver file",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"test"},
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.DisableDomain(ctx, args[0], notice("remove"))
			return domainResult(res), err
		}),
	})
	return cmd
}

// dnsCmd is hidden: it is what the DNS launchd agent runs.
func (a *app) dnsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "dns", Hidden: true, Short: "Covo's local DNS server"}
	var addr string
	var zones []string
	serve := &cobra.Command{
		Use:   "serve",
		Short: "Answer DNS queries for the given zones with loopback addresses",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			fmt.Fprintf(a.stderr, "covo dns: serving %v on %s\n", zones, addr)
			if err := (dns.Server{Zones: zones}).ListenAndServe(ctx, addr); err != nil && ctx.Err() == nil {
				return err
			}
			return nil
		},
	}
	serve.Flags().StringVar(&addr, "addr", "127.0.0.1:53535", "UDP and TCP address to listen on")
	serve.Flags().StringArrayVar(&zones, "zone", nil, "zone to answer for (repeatable)")
	cmd.AddCommand(serve)
	return cmd
}
