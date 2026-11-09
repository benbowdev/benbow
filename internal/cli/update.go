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

type updatesResult core.UpdatesStatus

func (r updatesResult) Text(w io.Writer) {
	for _, u := range r.Applied {
		fmt.Fprintf(w, "%-10s %s → %s\n", u.Component, u.Current, u.Available)
	}
	if len(r.Applied) > 0 && !r.Live {
		fmt.Fprintln(w, "Benbow is stopped: the new versions run with `benbow start`.")
	}
	if !r.Auto {
		fmt.Fprintln(w, "The daily automatic check is off (`benbow update auto on`).")
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
	fmt.Fprintln(w, "Install with `benbow update apply [component...]`.")
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
	var auto bool
	check := &cobra.Command{
		Use:   "check",
		Short: "Download and verify the signed manifest; install nothing",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			if auto {
				st, err := c.AutoCheckUpdates(ctx)
				return updatesResult(st), err
			}
			st, err := c.CheckUpdates(ctx)
			return updatesResult(st), err
		}),
	}
	// The daemon's daily check (release asset, then main), for tests.
	check.Flags().BoolVar(&auto, "auto", false, "run the automatic check instead")
	_ = check.Flags().MarkHidden("auto")
	cmd.AddCommand(check, &cobra.Command{
		Use:       "auto on|off",
		Short:     "Turn the daily automatic check on or off (off: Benbow makes no update request by itself)",
		Args:      cobra.MatchAll(cobra.ExactArgs(1), cobra.OnlyValidArgs),
		ValidArgs: []string{"on", "off"},
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			st, err := c.SetAutoUpdates(ctx, args[0] == "on")
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
