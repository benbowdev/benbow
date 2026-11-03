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
	"github.com/getcovo/covo/internal/mcpserver"
	"github.com/getcovo/covo/internal/store"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

type mcpPermResult core.MCPPermissions

func (r mcpPermResult) Text(w io.Writer) {
	switch {
	case r.All:
		fmt.Fprintln(w, "AI agents may change everything (with a snapshot before each change to a site).")
	case len(r.Sites) > 0:
		fmt.Fprintf(w, "AI agents may change: %s. Everything else is read-only.\n", strings.Join(r.Sites, ", "))
	default:
		fmt.Fprintln(w, "AI agents are read-only. Allow changes with: covo mcp allow <site>  (or * for everything)")
	}
}

type mcpLogResult []store.MCPAction

func (r mcpLogResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No changes by AI agents yet.")
	}
	for _, a := range r {
		state := "ok"
		switch {
		case a.Error != "":
			state = "refused/failed: " + a.Error
		case a.DryRun:
			state = "dry run"
		}
		snap := ""
		if a.Snapshot != "" {
			snap = "  snapshot " + a.Snapshot
		}
		fmt.Fprintf(w, "%s  %-18s %-14s %s%s\n", a.At.Local().Format("2006-01-02 15:04"), a.Tool, a.Site, state, snap)
	}
}

func (a *app) mcpCmd() *cobra.Command {
	cmd := a.mcpServeCmd()
	perm := func(write bool) *cobra.Command {
		use, short := "deny <site|*>", "Make a site (or everything) read-only for AI agents again"
		if write {
			use, short = "allow <site|*>", "Let AI agents change a site (* for everything, creating sites included)"
		}
		return &cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
				p, err := c.SetMCPPermission(ctx, args[0], write)
				return mcpPermResult(p), err
			}),
		}
	}
	var limit int
	logCmd := &cobra.Command{
		Use:   "log",
		Short: "What AI agents changed (or tried to), newest first",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			l, err := c.MCPActions(ctx, limit)
			return mcpLogResult(l), err
		}),
	}
	logCmd.Flags().IntVarP(&limit, "lines", "n", 50, "how many")
	cmd.AddCommand(perm(true), perm(false), logCmd, &cobra.Command{
		Use:   "status",
		Short: "What AI agents may change",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			p, err := c.GetMCPPermissions(ctx)
			return mcpPermResult(p), err
		}),
	})
	return cmd
}

func (a *app) mcpServeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run Covo's MCP server on stdin/stdout, for AI agents",
		Long: "Serves the Model Context Protocol over stdin/stdout: sites, PHP versions, logs,\n" +
			"databases, the PHP compatibility check and wp-cli. Add it to an agent, e.g.:\n\n" +
			"  claude mcp add covo -- covo mcp\n\n" +
			"Agents are read-only until you allow changes: covo mcp allow <site> (or *).\n" +
			"Before changing a site Covo takes a snapshot; covo mcp log shows what they did.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.ran = true
			if a.json {
				return errcode.Errorf(errcode.InvalidInput, "covo mcp speaks MCP on stdout: --json does not apply")
			}
			if err := mcpserver.New(core.Open).Run(cmd.Context(), &mcp.StdioTransport{}); err != nil {
				return fmt.Errorf("mcp server: %w", err)
			}
			return nil
		},
	}
}
