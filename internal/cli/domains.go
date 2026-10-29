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
	if h := d.Helper; h != nil {
		switch {
		case d.Enabled && h.Running:
			fmt.Fprintf(w, ".%-10s on, MAMP compatibility mode (helper %s, %d names in /etc/hosts)\n", d.Zone, h.Version, len(h.Names))
		case d.Enabled:
			fmt.Fprintf(w, ".%-10s on, but the helper does not answer: run `covo domains enable %s`\n", d.Zone, d.Zone)
		default:
			fmt.Fprintf(w, ".%-10s off (MAMP compatibility mode: `covo domains enable %s`)\n", d.Zone, d.Zone)
		}
		return
	}
	if d.Resolver == nil {
		return
	}
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
	if r.Zone == "test" && r.Enabled && !r.Applied {
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
Enabling and disabling each ask for one administrator authorization.

.local domains (e.g. shop.local) are a MAMP compatibility mode: Covo installs
a small helper that only manages Covo's own lines in /etc/hosts (127.0.0.1
and ::1). Installing it asks for one administrator authorization.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Domains(ctx)
			return domainsResult(res), err
		}),
	}
	notice := func(zone, action string) func() {
		return func() {
			if zone == "local" {
				fmt.Fprintln(a.stderr, "macOS will now ask for an administrator password to install Covo's helper, which manages Covo's lines in /etc/hosts.")
				return
			}
			fmt.Fprintf(a.stderr, "macOS will now ask for an administrator password to %s /etc/resolver/%s.\n", action, zone)
		}
	}
	cmd.AddCommand(&cobra.Command{
		Use:       "enable test|local",
		Short:     "Serve .test or .local domains (one administrator authorization)",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"test", "local"},
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.EnableDomain(ctx, args[0], notice(args[0], "create"))
			return domainResult(res), err
		}),
	}, &cobra.Command{
		Use:       "disable test|local",
		Short:     "Stop serving .test or .local domains and remove what Covo installed for them",
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"test", "local"},
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.DisableDomain(ctx, args[0], notice(args[0], "remove"))
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
