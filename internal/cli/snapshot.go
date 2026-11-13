// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"time"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/spf13/cobra"
)

type snapshotResult store.Snapshot

func (r snapshotResult) Text(w io.Writer) {
	db := "no database"
	if r.DBName != "" {
		db = "database " + r.DBName + " (" + humanBytes(r.Bytes) + ")"
	}
	fmt.Fprintf(w, "Snapshot %s of %s: files of %s, %s\n", r.ID, r.Site, r.Root, db)
	if r.PHPMinor != "" {
		fmt.Fprintf(w, "  PHP %s\n", r.PHPMinor)
	}
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}

type snapshotListResult []store.Snapshot

func (r snapshotListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No snapshots. Take one with: benbow snapshot create <site>")
	}
	for _, s := range r {
		fmt.Fprintf(w, "%s  %-11s %-8s %s\n", s.ID, s.Kind, humanBytes(s.Bytes), s.Note)
	}
}

type restoreResult core.RestoreResult

func (r restoreResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Restored %s to snapshot %s. The state before is snapshot %s: restore it to undo.\n",
		r.Restored.Site, r.Restored.ID, r.Before.ID)
	switch {
	case r.PHPRestored:
		fmt.Fprintf(w, "PHP is back to %s with the snapshot's php.ini values.\n", r.Restored.PHPMinor)
	case r.PHPDiffers:
		fmt.Fprintf(w, "The snapshot had PHP %s with other php.ini values: add --php to bring them back too.\n", r.Restored.PHPMinor)
	}
}

type snapshotKeepResult struct {
	Keep int `json:"keep"`
}

func (r snapshotKeepResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Benbow keeps the last %d snapshots of each site.\n", r.Keep)
}

func (a *app) snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save and restore a site's files and database",
		Long: "Snapshots save a site's project folder (APFS clones: no space until files\n" +
			"change; .git, node_modules and generated folders like .venv and .next are left out) and its database, in Benbow's folder.",
	}
	var note string
	create := &cobra.Command{
		Use:   "create <site>",
		Short: "Take a snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			s, err := c.CreateSnapshot(ctx, args[0], core.SnapshotManual, note)
			return snapshotResult(s), err
		}),
	}
	create.Flags().StringVar(&note, "note", "", "what this snapshot is for")
	var yes, restorePHP bool
	restore := &cobra.Command{
		Use:   "restore <site> <id>",
		Short: "Put the site's files and database back (the current state is saved first)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !yes && !a.confirm("Replace "+args[0]+"'s files and database with snapshot "+args[1]+"? The current state is saved first.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "restoring needs --yes")
			}
			r, err := c.RestoreSnapshot(ctx, args[0], args[1], core.RestoreOptions{PHP: restorePHP})
			return restoreResult(r), err
		}),
	}
	restore.Flags().BoolVar(&yes, "yes", false, "do not ask")
	restore.Flags().BoolVar(&restorePHP, "php", false, "also bring back the site's PHP version and php.ini values saved in the snapshot")
	cmd.AddCommand(a.snapshotUsageCmd(), a.snapshotPruneCmd())
	cmd.AddCommand(create, restore, &cobra.Command{
		Use:   "list <site>",
		Short: "List a site's snapshots, newest first",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			l, err := c.ListSnapshots(ctx, args[0])
			return snapshotListResult(l), err
		}),
	}, &cobra.Command{
		Use:     "delete <site> <id>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a snapshot",
		Args:    cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			return snapshotListResult{}, c.DeleteSnapshot(ctx, args[0], args[1])
		}),
	}, &cobra.Command{
		Use:   "keep [n]",
		Short: "Show or set how many snapshots are kept per site (default 10)",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if len(args) == 1 {
				n, err := strconv.Atoi(args[0])
				if err != nil {
					return nil, errcode.Errorf(errcode.InvalidInput, "keep needs a number")
				}
				if err := c.SetSnapshotKeep(ctx, n); err != nil {
					return nil, err
				}
			}
			n, err := c.SnapshotKeep(ctx)
			return snapshotKeepResult{n}, err
		}),
	})
	return cmd
}

type snapshotUsageResult core.SnapshotsUsage

func (r snapshotUsageResult) Text(w io.Writer) {
	if len(r.Snapshots) == 0 {
		fmt.Fprintln(w, "No snapshots.")
		return
	}
	fmt.Fprintf(w, "%-20s %-20s %-12s %10s %10s\n", "SITE", "SNAPSHOT", "KIND", "REAL", "APPARENT")
	for _, s := range r.Snapshots {
		kind := s.Kind
		if s.Protected {
			kind += "*"
		}
		fmt.Fprintf(w, "%-20s %-20s %-12s %10s %10s\n", s.Site, s.ID, kind, humanBytes(s.Private), humanBytes(s.Apparent))
	}
	fmt.Fprintf(w, "Total: %s real (%s apparent). Real = what deleting gives back; files are APFS clones of the site.\n",
		humanBytes(r.Private), humanBytes(r.Apparent))
}

func (a *app) snapshotUsageCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "usage [site]",
		Short: "Show the space snapshots really take, of one site or all",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			site := ""
			if len(args) == 1 {
				site = args[0]
			}
			u, err := c.SnapshotUsage(ctx, site)
			return snapshotUsageResult(u), err
		}),
	}
}

type pruneResult core.PruneResult

func (r pruneResult) Text(w io.Writer) {
	verb := "Deleted"
	if r.DryRun {
		verb = "Would delete"
	}
	for _, s := range r.Removed {
		fmt.Fprintf(w, "  %s %s (%s, %s)\n", s.Site, s.ID, s.Kind, humanBytes(s.Private))
	}
	fmt.Fprintf(w, "%s %d snapshots, %s.\n", verb, len(r.Removed), humanBytes(r.Freed))
	if r.Kept > 0 {
		fmt.Fprintf(w, "Kept %d used by an open agent session.\n", r.Kept)
	}
}

func (a *app) snapshotPruneCmd() *cobra.Command {
	var req core.PruneRequest
	var older string
	var yes bool
	cmd := &cobra.Command{
		Use:   "prune --older-than 30d",
		Short: "Delete snapshots older than a given age, of one site or all",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			d, err := parseAge(older)
			if err != nil {
				return nil, err
			}
			req.OlderThan = d
			if !req.DryRun && !yes && !a.confirm("Delete snapshots older than "+older+"? They cannot be brought back.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "pruning needs --yes (or --dry-run to see what would go)")
			}
			r, err := c.PruneSnapshots(ctx, req)
			return pruneResult(r), err
		}),
	}
	cmd.Flags().StringVar(&older, "older-than", "", "age, e.g. 30d, 2w, 12h")
	cmd.Flags().StringVar(&req.Site, "site", "", "only this site's snapshots")
	cmd.Flags().BoolVar(&req.DryRun, "dry-run", false, "list what would be deleted")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask")
	return cmd
}

// parseAge reads 30d, 2w or any Go duration (12h, 90m).
func parseAge(s string) (time.Duration, error) {
	bad := errcode.Errorf(errcode.InvalidInput, "--older-than wants an age like 30d, 2w or 12h")
	if s == "" {
		return 0, bad
	}
	unit := map[byte]time.Duration{'d': 24 * time.Hour, 'w': 7 * 24 * time.Hour}[s[len(s)-1]]
	if unit != 0 {
		n, err := strconv.Atoi(s[:len(s)-1])
		if err != nil || n <= 0 {
			return 0, bad
		}
		return time.Duration(n) * unit, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return 0, bad
	}
	return d, nil
}
