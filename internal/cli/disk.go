// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/benbowdev/benbow/internal/core"
)

type diskResult core.DiskReport

func (r diskResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Benbow uses %s in %s\n", humanBytes(r.Total), r.Home)
	for _, p := range r.Parts {
		line := fmt.Sprintf("  %-22s %10s", p.Title, humanBytes(p.Bytes))
		var parts []string
		for _, sub := range p.Parts {
			parts = append(parts, sub.Title+" "+humanBytes(sub.Bytes))
		}
		if len(parts) > 0 {
			line += "   " + strings.Join(parts, ", ")
		}
		if p.Name == "snapshots" && r.SnapshotsShared > 0 {
			line += fmt.Sprintf("   (plus %s shared with your sites, not counted)", humanBytes(r.SnapshotsShared))
		}
		fmt.Fprintln(w, line)
	}
}

func (a *app) diskCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "disk",
		Short: "Show the disk space Benbow takes: PHP, servers, databases, snapshots, caches",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			r, err := c.DiskUsage(ctx)
			return diskResult(r), err
		}),
	}
}
