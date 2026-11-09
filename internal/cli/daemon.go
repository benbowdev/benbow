// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/daemon"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/spf13/cobra"
)

type daemonStatusResult daemon.Status

func (s daemonStatusResult) Text(w io.Writer) {
	switch {
	case s.Answering:
		fmt.Fprintf(w, "Benbow daemon running (pid %d), API on %s\n", s.PID, s.Socket)
	case s.Loaded:
		fmt.Fprintf(w, "Benbow daemon loaded but not answering on %s\n", s.Socket)
	default:
		fmt.Fprintln(w, "Benbow daemon not running (the app starts it, or: benbow daemon start)")
	}
}

type daemonResult daemon.Result

func (r daemonResult) Text(w io.Writer) {
	daemonStatusResult(r.Status).Text(w)
}

func (a *app) daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Show, start or stop the daemon serving Benbow's local API",
		Long: `The daemon serves Benbow's local API (JSON over a Unix socket only you can
open) for the Benbow app. It runs as a launchd agent from the Benbow home and does
not start at login; benbow start and benbow stop do not touch it.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := daemon.Inspect(ctx, c)
			return daemonStatusResult(res), err
		}),
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "start",
		Short: "Start the daemon (restarts it if the benbow binary changed)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			exe, err := os.Executable()
			if err != nil {
				return nil, errcode.New(errcode.Internal, fmt.Errorf("locate benbow binary: %w", err))
			}
			res, err := daemon.Start(ctx, c, exe)
			return daemonResult(res), err
		}),
	}, &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := daemon.Stop(ctx, c)
			return daemonResult(res), err
		}),
	}, &cobra.Command{
		Use:    "run",
		Short:  "Serve the API in the foreground (what the launchd agent runs)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.ran = true
			return daemon.Run(cmd.Context())
		},
	})
	return cmd
}
