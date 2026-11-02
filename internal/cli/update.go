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

type updatesResult core.UpdatesStatus

func (r updatesResult) Text(w io.Writer) {
	for _, u := range r.Applied {
		fmt.Fprintf(w, "%-10s %s → %s\n", u.Component, u.Current, u.Available)
	}
	if len(r.Applied) > 0 && !r.Live {
		fmt.Fprintln(w, "Covo is stopped: the new versions run with `covo start`.")
	}
	if r.Error != "" {
		fmt.Fprintf(w, "Last check failed: %s\n", r.Error)
	}
	if len(r.Updates) == 0 {
		if len(r.Applied) == 0 {
			fmt.Fprintln(w, "Everything is up to date.")
		}
		return
	}
	fmt.Fprintln(w, "Available:")
	for _, u := range r.Updates {
		note := ""
		if u.Blocked != "" {
			note = "  (waiting: " + u.Blocked + ")"
		}
		fmt.Fprintf(w, "  %-10s %s → %s%s\n", u.Component, u.Current, u.Available, note)
	}
	fmt.Fprintln(w, "Install with `covo update apply [component...]`.")
}

func (a *app) updateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Show component updates (PHP, MySQL, Caddy, Mailpit, Adminer, wp-cli)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			st, err := c.Updates(ctx)
			return updatesResult(st), err
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "check",
		Short: "Download and verify the signed manifest; install nothing",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			st, err := c.CheckUpdates(ctx)
			return updatesResult(st), err
		}),
	}, &cobra.Command{
		Use:   "apply [component...]",
		Short: "Install updates (all, or e.g. caddy php-8.4) and restart what uses them",
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			st, err := c.ApplyUpdates(ctx, args)
			return updatesResult(st), err
		}),
	}, &cobra.Command{
		Use:   "rollback <component>",
		Short: "Go back to the version a component had before its last update",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			st, err := c.RollbackUpdate(ctx, args[0])
			return updatesResult(st), err
		}),
	})
	return cmd
}
