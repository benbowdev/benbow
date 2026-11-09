// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) wpCmd() *cobra.Command {
	return a.toolCmd(core.ToolWP, "Run wp-cli for a site, with its PHP version and Benbow's MySQL",
		"Run wp-cli for a site, e.g. `benbow wp shop plugin list`.")
}

func (a *app) composerCmd() *cobra.Command {
	return a.toolCmd(core.ToolComposer, "Run Composer in a site's project folder, with its PHP version",
		"Run Composer for a site, e.g. `benbow composer shop require guzzlehttp/guzzle`.\n"+
			"Composer 2.11 (2.2 LTS for PHP older than 7.3); its cache is in Benbow's folder.")
}

func (a *app) artisanCmd() *cobra.Command {
	return a.toolCmd(core.ToolArtisan, "Run Laravel's artisan for a site, with its PHP version",
		"Run artisan for a Laravel site, e.g. `benbow artisan shop migrate`.")
}

// toolCmd runs a site tool: every argument after the site name goes to the
// tool unchanged, and its output and exit code are passed through.
func (a *app) toolCmd(tool, short, long string) *cobra.Command {
	return &cobra.Command{
		Use:   tool + " <site> [" + tool + " arguments...]",
		Short: short,
		Long: long + "\n\nEvery argument after the site name goes to " + tool + " unchanged, and its\n" +
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
			a.exit, err = c.RunTool(cmd.Context(), args[0], tool, rest, os.Stdin, a.stdout, a.stderr)
			return err
		},
	}
}

type wordpressResult core.WordPressResult

func (r wordpressResult) Text(w io.Writer) {
	fmt.Fprintf(w, "WordPress %s ready at %s\n  folder    %s\n  database  %s\n  admin     %s\n  user      %s\n  password  %s\n",
		r.WordPress, r.URL, r.Path, r.Database, r.AdminURL, r.AdminUser, r.AdminPassword)
	fmt.Fprintln(w, "Save the admin password now: Benbow does not keep it.")
	if r.Blueprint != nil {
		blueprintApplyResult(*r.Blueprint).Text(w)
	}
}

func (a *app) wordpressCmd() *cobra.Command {
	var req core.WordPressRequest
	var group string
	var ini []string
	var yes bool
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
			var err error
			if req.Group, err = c.ResolveGroup(ctx, group); err != nil {
				return nil, err
			}
			if req.PHPSettings, err = iniFlags(ini); err != nil {
				return nil, err
			}
			if req.Blueprint != "" && !yes && !a.confirm(core.BlueprintNotice+"\nUse blueprint "+req.Blueprint+"?") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "a blueprint needs --yes")
			}
			res, err := c.NewWordPress(ctx, req)
			return wordpressResult(res), err
		}),
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "new or empty folder (default ~/Sites/<name>)")
	cmd.Flags().StringVar(&req.PHP, "php", "8.4", "PHP version")
	cmd.Flags().StringVar(&req.Title, "title", "", "site title (default: the name)")
	cmd.Flags().StringVar(&req.AdminUser, "admin-user", "admin", "administrator user name")
	cmd.Flags().StringVar(&req.AdminEmail, "admin-email", "", "administrator email (default admin@<domain>)")
	cmd.Flags().StringVar(&req.Domain, "domain", "", "local domain, e.g. blog.benbow.test (default: <name>.localhost)")
	cmd.Flags().StringSliceVar(&req.Aliases, "aliases", nil, "extra domains, comma separated")
	cmd.Flags().StringVar(&group, "group", "", "list the site in this group, e.g. Clients/Acme (see benbow group)")
	cmd.Flags().StringArrayVar(&ini, "ini", nil, "php.ini value of this site, name=value (repeatable)")
	cmd.Flags().StringVar(&req.Blueprint, "blueprint", "", "apply this blueprint (see benbow blueprint)")
	cmd.Flags().BoolVar(&req.AcceptChanges, "accept-changes", false, "with --blueprint: accept sources whose content changed since first use")
	cmd.Flags().BoolVar(&yes, "yes", false, "with --blueprint: do not ask")
	return cmd
}
