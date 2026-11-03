// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/php"
	"github.com/spf13/cobra"
)

func (a *app) phpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "php",
		Short: "Manage PHP versions",
	}
	cmd.AddCommand(a.phpInstallCmd(), a.phpListCmd(), a.phpXdebugCmd(), a.phpSettingsCmd())
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
	for _, s := range r.Support {
		switch s.Status {
		case php.SupportEOL:
			fmt.Fprintf(w, "PHP %s: no security fixes since %s, not recommended\n", s.Minor, s.SecurityUntil)
		case php.SupportSecurity:
			fmt.Fprintf(w, "PHP %s: security fixes only, until %s\n", s.Minor, s.SecurityUntil)
		}
	}
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

type phpSettingsResult core.PHPSettingsResult

func (r phpSettingsResult) Text(w io.Writer) {
	onOff := map[bool]string{true: "On", false: "Off"}
	fmt.Fprintf(w, "PHP %s\n  memory_limit         %s\n  upload_max_filesize  %s (post_max_size follows)\n  max_execution_time   %d\n  display_errors       %s\n",
		r.PHP, r.MemoryLimit, r.UploadMaxFilesize, r.MaxExecutionTime, onOff[r.DisplayErrors])
	if r.Changed && !r.Applied {
		fmt.Fprintln(w, "Saved: applied when Covo starts.")
	}
}

func (a *app) phpSettingsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "settings <version> [name=value...]",
		Short: "Show or change php.ini settings of a PHP version (e.g. memory_limit=1G)",
		Long: "Show or change php.ini settings of a PHP version.\n\nSettings: " +
			strings.Join(php.SettingNames, ", ") + ".\npost_max_size always follows upload_max_filesize.",
		Args: cobra.MinimumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if len(args) == 1 {
				s, err := c.GetPHPSettings(ctx, args[0])
				return phpSettingsResult{PHPSettings: s}, err
			}
			values := map[string]string{}
			for _, kv := range args[1:] {
				name, value, ok := strings.Cut(kv, "=")
				if !ok {
					return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%q: want name=value", kv))
				}
				values[name] = value
			}
			res, err := c.SetPHPSettings(ctx, args[0], values)
			return phpSettingsResult(res), err
		}),
	}
}
