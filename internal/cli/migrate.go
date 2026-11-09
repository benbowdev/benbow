// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/benbowdev/benbow/internal/core"
)

type covoMigrationResult core.CovoMigration

func (r covoMigrationResult) Text(w io.Writer) {
	verb := "Moved"
	if r.DryRun {
		verb = "Would move"
	}
	fmt.Fprintf(w, "%s Covo (%s) into Benbow (%s): %d sites.\n", verb, r.From, r.To, len(r.Sites))
	for _, s := range r.Steps {
		mark := map[string]string{"done": "✓", "planned": "·", "skipped": "–", "failed": "✗"}[s.Status]
		line := fmt.Sprintf("  %s %s", mark, s.Step)
		if s.Detail != "" {
			line += ": " + s.Detail
		}
		fmt.Fprintln(w, line)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintln(w, "  ! "+warn)
	}
	if !r.DryRun && r.Finished {
		fmt.Fprintln(w, "Done. You can move Covo.app to the Trash.")
	}
}

func (a *app) migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Move an installation of Covo, Benbow's former name",
	}
	var opts core.MigrateCovoOptions
	covo := &cobra.Command{
		Use:   "covo",
		Short: "Move Covo's sites, databases, snapshots and secrets into Benbow",
		Long: "Stops Covo and moves its folder (~/Library/Application Support/covo) to Benbow's: sites,\n" +
			"databases, snapshots and logs come along without being copied. Its keychain items are copied,\n" +
			"Benbow's own local CA is trusted (macOS asks for your password), the .test resolver is\n" +
			"rewritten (administrator password) and Benbow starts. Once every site answers, Covo's keychain\n" +
			"items and CA are removed and AI agents point at Benbow. If moving the folder or the keychain\n" +
			"items fails, Covo is left exactly as it was.",
		Args: cobra.NoArgs,
		RunE: a.run(func(ctx context.Context, _ []string) (Result, error) {
			opts.BeforePrompt = func(what string) {
				if !a.json {
					fmt.Fprintf(a.stderr, "macOS may ask for your password to %s.\n", what)
				}
			}
			r, err := core.MigrateFromCovo(ctx, opts)
			if err != nil && len(r.Steps) > 0 && !a.json {
				covoMigrationResult(r).Text(a.stderr)
			}
			return covoMigrationResult(r), err
		}),
	}
	covo.Flags().BoolVar(&opts.DryRun, "dry-run", false, "only show what would happen")
	covo.Flags().BoolVar(&opts.Finish, "finish", false, "only remove Covo's leftovers (after a migration whose site check failed)")
	covo.Flags().BoolVar(&opts.NoTrust, "no-trust", false, "leave the certificates' trust alone (run `benbow trust` later)")
	cmd.AddCommand(covo)
	return cmd
}
