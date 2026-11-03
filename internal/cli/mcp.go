// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"fmt"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mcpserver"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
)

func (a *app) mcpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Run Covo's MCP server on stdin/stdout, for AI agents",
		Long: "Serves the Model Context Protocol over stdin/stdout: sites, PHP versions, logs,\n" +
			"databases, the PHP compatibility check and wp-cli. Add it to an agent, e.g.:\n\n" +
			"  claude mcp add covo -- covo mcp\n\n" +
			"It never deletes sites or databases.",
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
