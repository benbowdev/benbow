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

type statusResult core.Status

func (s statusResult) Text(w io.Writer) {
	state := "stopped"
	if s.Running {
		state = "running"
	}
	fmt.Fprintf(w, "Covo is %s", state)
	if s.HTTPSPort != 0 {
		fmt.Fprintf(w, " (HTTPS port %d)", s.HTTPSPort)
	}
	fmt.Fprintln(w)
	for _, svc := range s.Services {
		pid := ""
		if svc.PID != 0 {
			pid = fmt.Sprintf("pid %d", svc.PID)
		}
		fmt.Fprintf(w, "  %-10s %-8s %s\n", svc.Name, running(svc.Running), pid)
	}
	for _, o := range s.Orphans {
		fmt.Fprintf(w, "  orphan agent %s (run covo start or covo stop)\n", o)
	}
	if len(s.Sites) == 0 {
		fmt.Fprintln(w, "No sites. Add one with: covo site add <name> --php 8.4")
	}
	for _, site := range s.Sites {
		fmt.Fprintf(w, "  %-14s PHP %-4s %s\n", site.Name, site.PHPMinor, site.URL)
	}
}

func running(b bool) string {
	if b {
		return "running"
	}
	return "stopped"
}

type startResult core.StartResult

func (r startResult) Text(w io.Writer) {
	if r.PortAssigned {
		fmt.Fprintf(w, "Using HTTPS port %d\n", r.HTTPSPort)
	}
	for _, c := range r.Changes {
		if c.Action != "unchanged" {
			fmt.Fprintf(w, "%s %s\n", c.Action, c.Label)
		}
	}
	statusResult(r.Status).Text(w)
}

type stopResult core.StopResult

func (r stopResult) Text(w io.Writer) {
	if len(r.Changes) == 0 {
		fmt.Fprintln(w, "Nothing was running.")
	}
	for _, c := range r.Changes {
		fmt.Fprintf(w, "%s %s\n", c.Action, c.Label)
	}
}

func (a *app) startCmd() *cobra.Command {
	var port int
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start Caddy and the PHP versions your sites use",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Start(ctx, port)
			return startResult(res), err
		}),
	}
	cmd.Flags().IntVar(&port, "https-port", 0, "change the HTTPS port (default: 8443, or the first free port after it)")
	return cmd
}

func (a *app) stopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop all Covo services",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Stop(ctx)
			return stopResult(res), err
		}),
	}
}

func (a *app) statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show services, sites and their URLs",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Status(ctx)
			return statusResult(res), err
		}),
	}
}
