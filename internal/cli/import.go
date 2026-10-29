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

type importResult core.ImportPlan

func (r importResult) Text(w io.Writer) {
	verb := "Created"
	if r.DryRun {
		verb = "Would create"
	}
	fmt.Fprintf(w, "MAMP PRO settings: %s (read from a copy; MAMP is not modified)\n\n", r.Source)
	for _, it := range r.Items {
		if it.Action != core.ImportCreate {
			continue
		}
		fmt.Fprintf(w, "%s %-28s PHP %-4s %s\n", verb, it.Domain, it.PHP, it.MAMP.DocumentRoot)
		for _, warn := range it.Warnings {
			fmt.Fprintf(w, "    ! %s\n", warn)
		}
	}
	for _, it := range r.Items {
		if it.Action == core.ImportSkip {
			fmt.Fprintf(w, "Skip   %-28s %s\n", it.MAMP.ServerName, it.Reason)
		}
	}
	fmt.Fprintf(w, "\n%d to create, %d skipped.\n", r.Create, r.Skip)
	if len(r.Databases) > 0 {
		fmt.Fprintf(w, "Databases are not imported (export them from MAMP): %s\n", strings.Join(r.Databases, ", "))
	}
	if r.NeedsLocal {
		fmt.Fprintln(w, "Keeping .local names needs `covo domains enable local` first; or pass --localhost.")
	}
	if r.DryRun && r.Create > 0 {
		fmt.Fprintln(w, "Nothing was changed. Run without --dry-run to import.")
	} else if !r.DryRun && r.Create > 0 && !r.Applied {
		fmt.Fprintln(w, "Covo is not running: the sites go live with `covo start`.")
	}
}

func (a *app) importCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "import",
		Short: "Import sites from another tool",
	}
	var opts core.ImportOptions
	mampCmd := &cobra.Command{
		Use:   "mamp",
		Short: "Recreate MAMP PRO's hosts as Covo sites (MAMP is never modified)",
		Long: `Reads MAMP PRO's host list from a temporary copy of its settings and creates
one Covo site per host, with the same name and document root. MAMP PRO itself
is never modified and can keep running.

.local names are kept, which needs the MAMP compatibility mode
(covo domains enable local); --localhost serves them as <name>.localhost
instead. PHP versions Covo does not offer (7.4 and older) map to PHP 8.1 with
a warning. Databases are listed, not imported.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.ImportMAMP(ctx, opts)
			return importResult(res), err
		}),
	}
	mampCmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "show what would be created, change nothing")
	mampCmd.Flags().BoolVar(&opts.Localhost, "localhost", false, "serve sites as <name>.localhost instead of <name>.local")
	cmd.AddCommand(mampCmd)
	return cmd
}
