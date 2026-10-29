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
	cmd.AddCommand(a.phpInstallCmd(), a.phpListCmd(), a.phpXdebugCmd())
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

type xdebugListResult []core.XdebugStatus

func (r xdebugListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No PHP versions installed.")
	}
	for _, x := range r {
		writeXdebug(w, x)
	}
}

func writeXdebug(w io.Writer, x core.XdebugStatus) {
	switch {
	case !x.Available:
		fmt.Fprintf(w, "PHP %-6s Xdebug not available for PHP %s\n", x.PHP, x.PHPVersion)
	case x.Enabled:
		fmt.Fprintf(w, "PHP %-6s Xdebug %s on (mode debug, trigger, IDE on 127.0.0.1:9003)\n", x.PHP, x.Version)
	default:
		fmt.Fprintf(w, "PHP %-6s Xdebug %s off\n", x.PHP, x.Version)
	}
}

type xdebugResult core.XdebugResult

func (r xdebugResult) Text(w io.Writer) {
	writeXdebug(w, r.XdebugStatus)
	if r.Changed && !r.Applied {
		fmt.Fprintln(w, "Covo is not running: PHP picks this up with `covo start`.")
	}
}

func (a *app) phpXdebugCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "xdebug",
		Short: "Show or toggle Xdebug per PHP version",
		Long: `Xdebug runs in step-debugging mode and only starts when triggered (browser
extension, or an XDEBUG_TRIGGER cookie or parameter). Your IDE listens on
127.0.0.1:9003. Turning it on or off restarts that PHP version.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Xdebug(ctx)
			return xdebugListResult(res), err
		}),
	}
	for _, on := range []bool{true, false} {
		use, short := "on <version>", "Turn Xdebug on for a PHP version (e.g. 8.4)"
		if !on {
			use, short = "off <version>", "Turn Xdebug off for a PHP version"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
				res, err := c.SetXdebug(ctx, args[0], on)
				return xdebugResult(res), err
			}),
		})
	}
	return cmd
}
