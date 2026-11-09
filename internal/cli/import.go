// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/importers"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/spf13/cobra"
)

type importResult core.ImportPlan

func (r importResult) Text(w io.Writer) {
	verb := "Created"
	if r.DryRun {
		verb = "Would create"
	}
	fmt.Fprintf(w, "%s: %s (only read)\n\n", importers.Title(r.Kind), r.Source)
	for _, it := range r.Items {
		if it.Action != core.ImportCreate {
			continue
		}
		fmt.Fprintf(w, "%s %-28s PHP %-4s %s\n", verb, it.Domain, it.PHP, it.MAMP.DocumentRoot)
		if it.Database != "" {
			fmt.Fprintf(w, "    database %s linked (already on Benbow's MySQL)\n", it.Database)
		}
		for _, warn := range it.Warnings {
			fmt.Fprintf(w, "    ! %s\n", warn)
		}
		if m := it.Migration; m != nil {
			switch {
			case m.Error != "":
				fmt.Fprintf(w, "    migration failed, undone: %s\n", m.Error)
			case m.Problem != "":
				fmt.Fprintf(w, "    not migrated: %s\n", m.Problem)
				for k, v := range m.Manual {
					fmt.Fprintf(w, "      set %s to %s\n", k, v)
				}
			default:
				state := "would"
				if m.Done {
					state = "did"
				}
				fmt.Fprintf(w, "    migration (%s):\n", state)
				for _, step := range m.Steps {
					fmt.Fprintf(w, "      - %s\n", step)
				}
				for _, warn := range m.Warnings {
					fmt.Fprintf(w, "      ! %s\n", warn)
				}
			}
		}
	}
	for _, it := range r.Items {
		if it.Action == core.ImportSkip {
			fmt.Fprintf(w, "Skip   %-28s %s\n", it.MAMP.ServerName, it.Reason)
		}
	}
	fmt.Fprintf(w, "\n%d to create, %d skipped.\n", r.Create, r.Skip)
	if r.Migrated > 0 {
		fmt.Fprintf(w, "%d migrated: they now work from Benbow. The project files are shared with %s, whose sites no longer reach their old database. Undo one with `benbow import undo <site>`.\n",
			r.Migrated, importers.Title(r.Kind))
	}
	if r.NeedsLocal {
		fmt.Fprintln(w, "Keeping .local names needs `benbow domains enable local` first; or pass --localhost.")
	}
	if r.DryRun && r.Create > 0 {
		fmt.Fprintln(w, "Nothing was changed. Run without --dry-run to import.")
	} else if !r.DryRun && r.Create > 0 && !r.Applied {
		fmt.Fprintln(w, "Benbow is not running: the sites go live with `benbow start`.")
	}
}

type undoResult store.Migration

func (r undoResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Undid the migration of %s", r.Site)
	if r.BackupPath != "" {
		fmt.Fprintf(w, ": %s restored", r.ConfigPath)
	}
	if r.CreatedDB {
		fmt.Fprintf(w, ", database %s deleted", r.DBName)
	}
	fmt.Fprintln(w, ".")
}

func (a *app) importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import sites from another tool (MAMP PRO, Local, Herd, Valet, XAMPP)",
	}
	for _, source := range importers.Names {
		cmd.AddCommand(a.importSourceCmd(source))
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "undo <site>",
		Short: "Undo a site's migration: restore its configuration, delete the copied database",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			m, err := c.UndoMigration(ctx, args[0])
			return undoResult(m), err
		}),
	})
	return cmd
}

func (a *app) importSourceCmd(source string) *cobra.Command {
	var opts core.ImportOptions
	var askPassword bool
	cmd := &cobra.Command{
		Use:   source,
		Short: "Recreate " + importers.Title(source) + "'s sites as Benbow sites (" + importers.Title(source) + " is never modified)",
		Long: `Reads ` + importers.Title(source) + `'s site list (its files are only read) and creates one Benbow
site per host, same name and folder. The tool itself is never modified and
can keep running. .local names are kept (benbow domains enable local), or
served as <name>.localhost with --localhost.

--migrate also brings each new site to work from Benbow: its database is
copied from the tool's MySQL (only read: the tool's MySQL must be running,
Benbow never starts it) or loaded from --dump, the site gets its own MySQL
user, wp-config.php or .env is backed up in Benbow's folder and pointed at
Benbow, and the old address is replaced in the database. The project files
stay shared: afterwards the tool's copy of the site no longer reaches its
database. ` + "`benbow import undo <site>`" + ` puts things back.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			opts.Source = source
			if askPassword {
				pw, err := a.readSecret("Password of " + importers.Title(source) + "'s MySQL user " + opts.Migration.SourceUser + ": ")
				if err != nil {
					return nil, err
				}
				opts.Migration.SourcePassword = pw
			}
			if opts.Migrate && !opts.DryRun && !a.json && a.stdin != nil {
				if !a.confirm("After the migration these sites work from Benbow; opened from " + importers.Title(source) +
					" they will no longer reach their database (the project files are shared). Continue?") {
					return nil, errcode.Errorf(errcode.ConfirmationRequired, "migration not confirmed")
				}
			}
			res, err := c.Import(ctx, opts)
			return importResult(res), err
		}),
	}
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show what would happen, change nothing")
	cmd.Flags().BoolVar(&opts.Localhost, "localhost", false, "serve sites as <name>.localhost instead of their .local names")
	cmd.Flags().StringSliceVar(&opts.Sites, "site", nil, "only these hosts (server name or site name; repeatable)")
	cmd.Flags().BoolVar(&opts.Migrate, "migrate", false, "also copy databases and point wp-config.php/.env at Benbow")
	cmd.Flags().StringVar(&opts.Migration.Existing, "existing", "use", "when Benbow already has the database: use, recopy (after a backup) or skip")
	cmd.Flags().StringVar(&opts.Migration.DumpFile, "dump", "", "load this .sql or .sql.gz instead of reading the source's MySQL (one --site)")
	cmd.Flags().StringVar(&opts.Migration.SourceUser, "source-user", "", "MySQL user on the source (default: the one in wp-config.php/.env)")
	cmd.Flags().BoolVar(&askPassword, "source-password", false, "ask for the source user's password")
	return cmd
}
