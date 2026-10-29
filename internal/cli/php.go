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

func (a *app) phpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "php",
		Short: "Manage PHP versions",
	}
	cmd.AddCommand(a.phpInstallCmd(), a.phpListCmd())
	return cmd
}

type phpInstallResult core.PHPInstall

func (r phpInstallResult) Text(w io.Writer) {
	verb := "Installed"
	if !r.Fresh {
		verb = "Already installed:"
	}
	fmt.Fprintf(w, "%s PHP %s\n  %s\n", verb, r.Version, r.Binary)
}

func (a *app) phpInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install <version>",
		Short: "Download, verify and install a PHP version (e.g. 8.4)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.InstallPHP(ctx, args[0])
			return phpInstallResult(res), err
		}),
	}
}

type phpListResult core.PHPList

func (r phpListResult) Text(w io.Writer) {
	if len(r.Installed) == 0 {
		fmt.Fprintln(w, "No PHP versions installed.")
	}
	for _, v := range r.Installed {
		fmt.Fprintf(w, "%-8s %s\n", v.Version, v.Path)
	}
	fmt.Fprintf(w, "Available: %s\n", strings.Join(r.Available, ", "))
}

func (a *app) phpListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List installed and available PHP versions",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.ListPHP(ctx)
			return phpListResult(res), err
		}),
	}
}
