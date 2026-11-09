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

type laravelResult core.LaravelResult

func (r laravelResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Laravel %s ready at %s\n  project   %s\n  database  %s\n", r.Laravel, r.URL, r.AppRoot, r.Database)
}

func (a *app) laravelCmd() *cobra.Command {
	var req core.LaravelRequest
	var group string
	var ini []string
	cmd := &cobra.Command{
		Use:   "laravel <name>",
		Short: "Create a new Laravel project and serve it, with its .env ready",
		Long: "Runs composer create-project laravel/laravel, adds the site (served from public/)\n" +
			"and sets APP_URL, the database (a new one with its own user when MySQL is\n" +
			"running, else SQLite) and Mailpit in .env.",
		Args: cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req.Name = args[0]
			var err error
			if req.Path != "" {
				if req.Path, err = absDir(req.Path); err != nil {
					return nil, err
				}
			}
			if req.Group, err = c.ResolveGroup(ctx, group); err != nil {
				return nil, err
			}
			if req.PHPSettings, err = iniFlags(ini); err != nil {
				return nil, err
			}
			res, err := c.NewLaravel(ctx, req)
			return laravelResult(res), err
		}),
	}
	cmd.Flags().StringVar(&req.Path, "path", "", "new or empty folder (default ~/Sites/<name>)")
	cmd.Flags().StringVar(&req.PHP, "php", "8.4", "PHP version (8.2 or newer)")
	cmd.Flags().StringVar(&req.Database, "database", "", `"new" (own MySQL database) or "sqlite" (default: new if MySQL is running)`)
	cmd.Flags().StringVar(&req.Engine, "engine", "", "engine of the new database: mysql or pgsql (default: whichever is running, MySQL first)")
	cmd.Flags().StringVar(&req.Domain, "domain", "", "local domain (default: <name>.localhost)")
	cmd.Flags().StringSliceVar(&req.Aliases, "aliases", nil, "extra domains, comma separated")
	cmd.Flags().StringVar(&group, "group", "", "list the site in this group")
	cmd.Flags().StringArrayVar(&ini, "ini", nil, "php.ini value of this site, name=value (repeatable)")
	return cmd
}
