// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/getcovo/covo/internal/core"
)

// appCmd holds what the launchd agents of apps run. Users manage apps with
// covo site (add --command, restart, logs --app).
func (a *app) appCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "app",
		Short:  "Run app sites' dev servers (used by Covo's launchd agents)",
		Hidden: true,
	}
	var companion bool
	run := &cobra.Command{
		Use:   "run <site>",
		Short: "Run a site's command until it exits or the agent is unloaded",
		Args:  cobra.ExactArgs(1),
		// The output is the app log and the exit status goes to launchd:
		// no result envelope.
		RunE: func(_ *cobra.Command, args []string) error {
			ctx := context.Background()
			c, err := core.Open(ctx)
			if err != nil {
				fmt.Fprintln(os.Stderr, "[covo]", err)
				os.Exit(1)
			}
			code := c.RunApp(ctx, args[0], companion)
			_ = c.Close()
			os.Exit(code)
			return nil
		},
	}
	run.Flags().BoolVar(&companion, "companion", false, "run the PHP site's companion")
	cmd.AddCommand(run)
	return cmd
}
