// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
	"github.com/spf13/cobra"
)

type snapshotResult store.Snapshot

func (r snapshotResult) Text(w io.Writer) {
	db := "no database"
	if r.DBName != "" {
		db = "database " + r.DBName + " (" + humanBytes(r.Bytes) + ")"
	}
	fmt.Fprintf(w, "Snapshot %s of %s: files of %s, %s\n", r.ID, r.Site, r.Root, db)
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
		fmt.Fprintln(w, "No snapshots. Take one with: covo snapshot create <site>")
	}
	for _, s := range r {
		fmt.Fprintf(w, "%s  %-11s %-8s %s\n", s.ID, s.Kind, humanBytes(s.Bytes), s.Note)
	}
}

type restoreResult core.RestoreResult

func (r restoreResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Restored %s to snapshot %s. The state before is snapshot %s: restore it to undo.\n",
		r.Restored.Site, r.Restored.ID, r.Before.ID)
}

type snapshotKeepResult struct {
	Keep int `json:"keep"`
}

func (r snapshotKeepResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Covo keeps the last %d snapshots of each site.\n", r.Keep)
}

func (a *app) snapshotCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "snapshot",
		Short: "Save and restore a site's files and database",
		Long: "Snapshots save a site's project folder (APFS clones: no space until files\n" +
			"change; .git and node_modules are left out) and its database, in Covo's folder.",
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
	var yes bool
	restore := &cobra.Command{
		Use:   "restore <site> <id>",
		Short: "Put the site's files and database back (the current state is saved first)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !yes && !a.confirm("Replace "+args[0]+"'s files and database with snapshot "+args[1]+"? The current state is saved first.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "restoring needs --yes")
			}
			r, err := c.RestoreSnapshot(ctx, args[0], args[1])
			return restoreResult(r), err
		}),
	}
	restore.Flags().BoolVar(&yes, "yes", false, "do not ask")
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
