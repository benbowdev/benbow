// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"slices"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// AI agents (MCP) are read-only unless the user allows writes, for one site
// or for everything ("*"). Creating sites needs "*".

// MCPAll is the scope of every site.
const MCPAll = "*"

// MCPPermissions is what agents may change.
type MCPPermissions struct {
	// All allows writes everywhere, sites included.
	All bool `json:"all"`
	// Sites lists the sites agents may change.
	Sites []string `json:"sites"`
	// Temporary lists the sites agents may change only while the agent
	// session Covo opened for "Work with an agent" lasts.
	Temporary []string `json:"temporary"`
}

// GetMCPPermissions returns what agents may change.
func (c *Core) GetMCPPermissions(ctx context.Context) (MCPPermissions, error) {
	m, err := c.Store.MCPPermissions(ctx)
	if err != nil {
		return MCPPermissions{}, err
	}
	p := MCPPermissions{All: m[MCPAll], Sites: []string{}, Temporary: []string{}}
	for scope, w := range m {
		if w && scope != MCPAll {
			p.Sites = append(p.Sites, scope)
		}
	}
	slices.Sort(p.Sites)
	open, err := c.Store.AgentSessions(ctx, true, 1000)
	if err != nil {
		return p, err
	}
	for _, s := range open {
		if s.WriteGrant {
			p.Temporary = append(p.Temporary, s.Site)
		}
	}
	slices.Sort(p.Temporary)
	return p, nil
}

// SetMCPPermission allows or forbids agents to change a site ("*": all).
func (c *Core) SetMCPPermission(ctx context.Context, scope string, write bool) (MCPPermissions, error) {
	if scope != MCPAll {
		if _, err := c.Store.Site(ctx, scope); err != nil {
			return MCPPermissions{}, err
		}
	}
	if err := c.Store.SetMCPPermission(ctx, scope, write); err != nil {
		return MCPPermissions{}, err
	}
	return c.GetMCPPermissions(ctx)
}

// CheckMCPWrite fails unless agents may change site ("" for changes that
// are not about one site, such as creating one).
func (c *Core) CheckMCPWrite(ctx context.Context, site string) error {
	p, err := c.GetMCPPermissions(ctx)
	if err != nil {
		return err
	}
	if p.All || (site != "" && (slices.Contains(p.Sites, site) || slices.Contains(p.Temporary, site))) {
		return nil
	}
	what := "changes outside one site"
	if site != "" {
		what = "changes to " + site
	}
	return errcode.Newf(errcode.Forbidden,
		"AI agents are read-only for %s: the user can allow them in Covo → Settings → AI agents, or with `covo mcp allow %s`",
		what, map[bool]string{true: site, false: MCPAll}[site != ""])

}

// MCPActions returns the log of what agents did, newest first.
func (c *Core) MCPActions(ctx context.Context, limit int) ([]store.MCPAction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return c.Store.MCPActions(ctx, limit)
}
