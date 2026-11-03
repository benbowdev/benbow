// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"fmt"
	"time"
)

// MCPAction is one change an AI agent asked for through Covo's MCP server.
type MCPAction struct {
	ID       int64     `json:"id"`
	At       time.Time `json:"at"`
	Tool     string    `json:"tool"`
	Site     string    `json:"site,omitempty"`
	Args     string    `json:"args"`
	OK       bool      `json:"ok"`
	Error    string    `json:"error,omitempty"`
	Snapshot string    `json:"snapshot,omitempty"`
	DryRun   bool      `json:"dry_run,omitempty"`
}

// MCPPermissions maps scopes ("*" or a site name) to write permission.
func (s *Store) MCPPermissions(ctx context.Context) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scope, write FROM mcp_permissions`)
	if err != nil {
		return nil, fmt.Errorf("read MCP permissions: %w", err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var scope string
		var w bool
		if err := rows.Scan(&scope, &w); err != nil {
			return nil, fmt.Errorf("read MCP permissions: %w", err)
		}
		out[scope] = w
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read MCP permissions: %w", err)
	}
	return out, nil
}

// SetMCPPermission allows or forbids writes for a scope.
func (s *Store) SetMCPPermission(ctx context.Context, scope string, write bool) error {
	var err error
	if write {
		_, err = s.db.ExecContext(ctx, `INSERT INTO mcp_permissions (scope, write) VALUES (?, 1) ON CONFLICT(scope) DO UPDATE SET write = 1`, scope)
	} else {
		_, err = s.db.ExecContext(ctx, `DELETE FROM mcp_permissions WHERE scope = ?`, scope)
	}
	if err != nil {
		return fmt.Errorf("set MCP permission: %w", err)
	}
	return nil
}

// AddMCPAction logs an action, keeping the newest 1000.
func (s *Store) AddMCPAction(ctx context.Context, a MCPAction) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO mcp_actions (at, tool, site, args, ok, error, snapshot, dry_run) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.At.UTC().Format(time.RFC3339), a.Tool, a.Site, a.Args, a.OK, a.Error, a.Snapshot, a.DryRun)
	if err != nil {
		return fmt.Errorf("log MCP action: %w", err)
	}
	_, _ = s.db.ExecContext(ctx, `DELETE FROM mcp_actions WHERE id <= (SELECT MAX(id) - 1000 FROM mcp_actions)`)
	return nil
}

// MCPActions returns the newest actions first.
func (s *Store) MCPActions(ctx context.Context, limit int) ([]MCPAction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, tool, site, args, ok, error, snapshot, dry_run FROM mcp_actions ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("read MCP actions: %w", err)
	}
	defer rows.Close()
	out := []MCPAction{}
	for rows.Next() {
		var a MCPAction
		var at string
		if err := rows.Scan(&a.ID, &at, &a.Tool, &a.Site, &a.Args, &a.OK, &a.Error, &a.Snapshot, &a.DryRun); err != nil {
			return nil, fmt.Errorf("read MCP actions: %w", err)
		}
		a.At, _ = time.Parse(time.RFC3339, at)
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read MCP actions: %w", err)
	}
	return out, nil
}
