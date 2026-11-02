// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

func (a *app) wpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "wp <site> [wp-cli arguments...]",
		Short: "Run wp-cli for a site, with its PHP version and Covo's MySQL",
		Long: "Run wp-cli for a site, e.g. `covo wp shop plugin list`.\n\n" +
			"Every argument after the site name goes to wp-cli unchanged, and its\n" +
			"output and exit code are passed through (no --json envelope).",
		Args:               cobra.MinimumNArgs(1),
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] == "-h" || args[0] == "--help" {
				return cmd.Help()
			}
			a.ran = true
			c, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			rest := args[1:]
			if len(rest) > 0 && rest[0] == "--" {
				rest = rest[1:]
			}
			a.exit, err = c.RunWP(cmd.Context(), args[0], rest, os.Stdin, a.stdout, a.stderr)
			return err
		},
	}
}

type wordpressResult core.WordPressResult

func (r wordpressResult) Text(w io.Writer) {
	fmt.Fprintf(w, "WordPress %s ready at %s\n  folder    %s\n  database  %s\n  admin     %s\n  user      %s\n  password  %s\n",
		r.WordPress, r.URL, r.Path, r.Database, r.AdminURL, r.AdminUser, r.AdminPassword)
	fmt.Fprintln(w, "Save the admin password now: Covo does not keep it.")
}

func (a *app) wordpressCmd() *cobra.Command {
	var req core.WordPressRequest
	cmd := &cobra.Command{
		Use:   "wordpress <name>",
		Short: "Create a new WordPress site: folder, database, wp-config.php, installation",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req.Name = args[0]
			if req.Path != "" {
				dir, err := absDir(req.Path)
				if err != nil {
					return nil, err
				}
				req.Path = dir
			}
			res, err := c.NewWordPress(ctx, req)
			return wordpressResult(res), err
		}),
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "new or empty folder (default ~/Sites/<name>)")
	cmd.Flags().StringVar(&req.PHP, "php", "8.4", "PHP version")
	cmd.Flags().StringVar(&req.Title, "title", "", "site title (default: the name)")
	cmd.Flags().StringVar(&req.AdminUser, "admin-user", "admin", "administrator user name")
	cmd.Flags().StringVar(&req.AdminEmail, "admin-email", "", "administrator email (default admin@<name>.localhost)")
	return cmd
}
