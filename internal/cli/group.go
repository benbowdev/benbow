// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) groupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "group",
		Short: "Organize the site list in nested groups",
		Long:  "Groups only organize the list: they change nothing about how sites are served.\nPaths use / between levels, e.g. Clients/Acme.",
	}
	cmd.AddCommand(a.groupListCmd(), a.groupAddCmd(), a.groupRenameCmd(), a.groupMoveCmd(), a.groupRemoveCmd(),
		a.groupEnableCmd(true), a.groupEnableCmd(false))
	return cmd
}

type groupListResult []core.GroupInfo

func (r groupListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No groups. Create one with: covo group add Clients/Acme")
	}
	for _, g := range r {
		depth := strings.Count(g.Path, "/")
		fmt.Fprintf(w, "%s%s\n", strings.Repeat("  ", depth), g.Name)
	}
}

func (a *app) groupListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List groups",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.ListGroups(ctx)
			return groupListResult(res), err
		}),
	}
}

type groupResult core.GroupInfo

func (r groupResult) Text(w io.Writer) { fmt.Fprintln(w, r.Path) }

func (a *app) groupAddCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "add <path>",
		Short: "Create a group, and any missing group above it (e.g. Clients/Acme)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.AddGroupPath(ctx, args[0])
			return groupResult(res), err
		}),
	}
}

func (a *app) groupRenameCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "rename <path> <new-name>",
		Short: "Rename a group",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			id, err := namedGroup(ctx, c, args[0])
			if err != nil {
				return nil, err
			}
			res, err := c.UpdateGroup(ctx, core.UpdateGroupRequest{ID: id, Name: args[1]})
			return groupResult(res), err
		}),
	}
}

func (a *app) groupMoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "move <path> <into>",
		Short: `Move a group into another one ("/" for the top level)`,
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			id, err := namedGroup(ctx, c, args[0])
			if err != nil {
				return nil, err
			}
			into, err := c.ResolveGroup(ctx, args[1])
			if err != nil {
				return nil, err
			}
			res, err := c.UpdateGroup(ctx, core.UpdateGroupRequest{ID: id, Parent: &into})
			return groupResult(res), err
		}),
	}
}

type groupRemoveResult core.GroupInfo

func (r groupRemoveResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Removed group %s; its sites and groups moved up one level.\n", r.Path)
}

func (a *app) groupRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <path>",
		Aliases: []string{"rm"},
		Short:   "Remove a group; its sites and groups move up one level (no site is removed)",
		Args:    cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			id, err := namedGroup(ctx, c, args[0])
			if err != nil {
				return nil, err
			}
			res, err := c.RemoveGroup(ctx, id)
			return groupRemoveResult(res), err
		}),
	}
}

// namedGroup resolves a path that must name a group, not the top level.
func namedGroup(ctx context.Context, c *core.Core, path string) (int64, error) {
	id, err := c.ResolveGroup(ctx, path)
	if err == nil && id == 0 {
		return 0, errcode.Errorf(errcode.InvalidInput, "name a group, e.g. Clients/Acme")
	}
	return id, err
}

type groupEnabledResult core.GroupEnabledResult

func (r groupEnabledResult) Text(w io.Writer) {
	state := "off"
	if r.Enabled {
		state = "on"
	}
	switch len(r.Changed) {
	case 0:
		fmt.Fprintf(w, "Every site in %s was already %s.\n", r.Group.Path, state)
	default:
		fmt.Fprintf(w, "Turned %s %d site(s) in %s: %s\n", state, len(r.Changed), r.Group.Path, strings.Join(r.Changed, ", "))
	}
	if r.Enabled && len(r.Archived) > 0 {
		fmt.Fprintf(w, "Archived, left off: %s\n", strings.Join(r.Archived, ", "))
	}
}

func (a *app) groupEnableCmd(enabled bool) *cobra.Command {
	use, short := "enable <path>", "Turn on every site in a group and its subgroups (archived sites stay off)"
	if !enabled {
		use, short = "disable <path>", "Turn off every site in a group and its subgroups"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			id, err := namedGroup(ctx, c, args[0])
			if err != nil {
				return nil, err
			}
			res, err := c.SetGroupEnabled(ctx, id, enabled)
			return groupEnabledResult(res), err
		}),
	}
}
