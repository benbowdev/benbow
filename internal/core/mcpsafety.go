// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
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
	// session Benbow opened for "Work with an agent" lasts.
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
		"AI agents are read-only for %s: the user can allow them in Benbow → Settings → AI agents, or with `benbow mcp allow %s`",
		what, map[bool]string{true: site, false: MCPAll}[site != ""])

}

// MCPActions returns the log of what agents did, newest first.
func (c *Core) MCPActions(ctx context.Context, limit int) ([]store.MCPAction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	return c.Store.MCPActions(ctx, limit)
}

// AgentChange is one change an AI agent asks for.
type AgentChange struct {
	Tool string
	// Site is the site changed ("" for a change outside any site, which
	// needs the permission for everything).
	Site string
	// Args are logged as given (secrets are the caller's to leave out).
	Args   string
	Agent  string
	DryRun bool
	// Risky takes a checkpoint first (the caller knows the tool's
	// arguments; a tool that takes its own snapshot passes false).
	Risky bool
}

// AgentChangeResult is what the agent is told besides the change's result.
type AgentChangeResult struct {
	// Checkpoint is the snapshot taken just before, if any.
	Checkpoint string
	// SessionStart is the agent session's starting snapshot, if any.
	SessionStart string
}

// RunAgentChange is the only way an agent changes anything: the user's
// permission for the site, the site's agent session (opened if needed,
// with its starting snapshot, and its database dump finished), a
// checkpoint if the change is risky, the change itself, and an entry in
// the action log, in this order. A dry run only checks the permission.
// fn takes the lock itself if it needs it.
func (c *Core) RunAgentChange(ctx context.Context, ch AgentChange, fn func() error) (AgentChangeResult, error) {
	var res AgentChangeResult
	act := store.MCPAction{At: time.Now(), Tool: ch.Tool, Site: ch.Site, Args: ch.Args, DryRun: ch.DryRun}
	logged := func(err error) error {
		act.OK = err == nil
		if err != nil {
			act.Error = err.Error()
		}
		act.Snapshot = res.Checkpoint
		_ = c.Store.AddMCPAction(context.WithoutCancel(ctx), act)
		return err
	}
	if err := c.CheckMCPWrite(ctx, ch.Site); err != nil {
		return res, logged(err)
	}
	if ch.Site != "" && !ch.DryRun {
		if _, err := c.Store.Site(ctx, ch.Site); err == nil {
			s, _, err := c.EnsureAgentSession(ctx, SessionRequest{Site: ch.Site, Agent: ch.Agent, Source: SessionFromCall})
			if err == nil {
				err = c.WaitSessionDB(ctx, ch.Site)
			}
			if err == nil && ch.Risky {
				var snap store.Snapshot
				if snap, err = c.Checkpoint(ctx, ch.Site, "before "+ch.Tool+" (AI agent)"); err == nil {
					res.Checkpoint = snap.ID
				}
			}
			if err != nil {
				return res, logged(fmt.Errorf("saving the site before the change failed, nothing was changed: %w", err))
			}
			res.SessionStart = s.BaseSnapshot
		}
	}
	return res, logged(fn())
}
