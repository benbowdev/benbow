// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) dbCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "db",
		Short: "Manage site databases on Covo's MySQL",
	}
	cmd.AddCommand(a.dbListCmd(), a.dbShowCmd(), a.dbCreateCmd(), a.dbLinkCmd(), a.dbUnlinkCmd(), a.dbQueryCmd())
	return cmd
}

type dbListResult []string

func (r dbListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No databases.")
	}
	for _, d := range r {
		fmt.Fprintln(w, d)
	}
}

func (a *app) dbListCmd() *cobra.Command {
	var engine string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List databases (the server must be running)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			if engine == core.EnginePostgres || engine == "postgres" {
				res, err := c.PostgresDatabases(ctx)
				return dbListResult(res), err
			}
			res, err := c.Databases(ctx)
			return dbListResult(res), err
		}),
	}
	cmd.Flags().StringVar(&engine, "engine", "mysql", "mysql or pgsql")
	return cmd
}

type siteDBResult core.SiteDatabase

func (r siteDBResult) Text(w io.Writer) {
	switch {
	case r.Dropped:
		fmt.Fprintf(w, "Deleted database %s from %s\n", r.Database, r.Site)
		return
	case r.Password == "":
		fmt.Fprintf(w, "%s no longer uses database %s (the database is kept)\n", r.Site, r.Database)
		return
	}
	engine := "MySQL"
	if r.Engine == core.EnginePostgres {
		engine = "PostgreSQL"
	}
	fmt.Fprintf(w, "%s  %s database %s\n  user      %s\n  host      %s (port %d)\n  socket    %s\n  password  covo db show %s --json\n",
		r.Site, engine, r.Database, r.User, r.Host, r.Port, r.Socket, r.Site)
}

func (a *app) dbShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show <site>",
		Short: "Show a site's database and how to connect (password with --json)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.GetSiteDatabase(ctx, args[0])
			return siteDBResult(res), err
		}),
	}
}

func (a *app) dbCreateCmd() *cobra.Command {
	var name, engine string
	cmd := &cobra.Command{
		Use:   "create <site>",
		Short: "Create a database and its own user for a site",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.CreateSiteDatabaseOn(ctx, args[0], name, engine)
			return siteDBResult(res), err
		}),
	}
	cmd.Flags().StringVar(&name, "name", "", "database name (default: the site name with underscores)")
	cmd.Flags().StringVar(&engine, "engine", "mysql", "mysql or pgsql")
	return cmd
}

func (a *app) dbLinkCmd() *cobra.Command {
	var engine string
	cmd := &cobra.Command{
		Use:   "link <site> <database>",
		Short: "Link an existing database to a site (connects as root or postgres)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.LinkSiteDatabaseOn(ctx, args[0], args[1], engine)
			return siteDBResult(res), err
		}),
	}
	cmd.Flags().StringVar(&engine, "engine", "mysql", "mysql or pgsql")
	return cmd
}

func (a *app) dbUnlinkCmd() *cobra.Command {
	var drop, yes bool
	cmd := &cobra.Command{
		Use:   "unlink <site>",
		Short: "Forget a site's database (--drop also deletes it)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if drop && !yes && !a.confirm("Delete the database of "+args[0]+" and all its data? This cannot be undone.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "deleting a database needs --yes")
			}
			res, err := c.UnlinkSiteDatabase(ctx, args[0], drop)
			return siteDBResult(res), err
		}),
	}
	cmd.Flags().BoolVar(&drop, "drop", false, "also delete the database and the site's own MySQL user")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before deleting")
	return cmd
}
