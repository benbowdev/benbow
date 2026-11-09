// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// Agent sessions: while an AI agent works on a site, Covo keeps the site's
// starting state (files and database) until the user keeps the changes or
// undoes them all. Inside a session Covo takes a checkpoint only before
// risky changes, so the starting point never falls out of the snapshot
// limit, and changes the agent makes with its own tools are covered too.

// Session sources.
const (
	SessionFromFolder = "mcp-folder" // the agent runs inside the site's folder
	SessionFromCall   = "mcp-call"   // first tool call naming a site it may change
	SessionFromLaunch = "launch"     // "Work with an agent" in Covo
	SessionFromUser   = "manual"     // covo agent session open, before using any agent
)

// Session outcomes.
const (
	SessionKeep    = "keep"
	SessionRevert  = "revert"
	SessionTimeout = "timeout"
)

const (
	settingSessionIdle    = "agent_session_idle_hours"
	defaultSessionIdle    = 24
	maxSessionCheckpoints = 20
	sessionDBWait         = 15 * time.Minute
)

// SessionRequest opens (or touches) a site's agent session.
type SessionRequest struct {
	Site   string
	Agent  string
	Source string
	// WriteGrant lets agents change the site until the session closes.
	WriteGrant   bool
	LaunchedTool string
	// Wait returns only once the starting database dump is written (the
	// CLI and "Work with an agent", which are not in a hurry).
	Wait bool
}

// SessionInfo is a session with its snapshots.
type SessionInfo struct {
	store.AgentSession
	URL         string           `json:"url,omitempty"`
	Base        *store.Snapshot  `json:"base,omitempty"`
	Checkpoints []store.Snapshot `json:"checkpoints"`
	// IdleHours is when an idle session closes by itself (0: never).
	IdleHours int `json:"idle_hours"`
}

// SessionIdleHours returns after how many idle hours a session closes,
// keeping the changes (0: never).
func (c *Core) SessionIdleHours(ctx context.Context) (int, error) {
	v, ok, err := c.Store.Setting(ctx, settingSessionIdle)
	if err != nil || !ok {
		return defaultSessionIdle, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return defaultSessionIdle, nil //nolint:nilerr // a bad value means the default
	}
	return n, nil
}

// SetSessionIdleHours sets it (0–720; 0: never).
func (c *Core) SetSessionIdleHours(ctx context.Context, h int) error {
	if h < 0 || h > 720 {
		return errcode.Errorf(errcode.InvalidInput, "idle hours between 0 (never) and 720")
	}
	return c.Store.SetSetting(ctx, settingSessionIdle, strconv.Itoa(h))
}

// EnsureAgentSession returns the site's open session, opening one (with
// its starting snapshot) if there is none. opened reports a new one.
func (c *Core) EnsureAgentSession(ctx context.Context, req SessionRequest) (s store.AgentSession, opened bool, err error) {
	if _, err := c.Store.Site(ctx, req.Site); err != nil {
		return s, false, err
	}
	_, _ = c.ExpireAgentSessions(ctx)
	err = c.locked(ctx, func() error {
		now := time.Now()
		if cur, err := c.Store.OpenAgentSession(ctx, req.Site); err == nil {
			s = cur
			s.LastActive = now
			if s.Agent == "" {
				s.Agent = req.Agent
			}
			if req.WriteGrant {
				s.WriteGrant = true
			}
			if req.LaunchedTool != "" {
				s.LaunchedTool, s.AgentRunning = req.LaunchedTool, true
			}
			return c.Store.UpdateAgentSession(ctx, s)
		} else if errcode.Code(err) != errcode.NotFound {
			return err
		}
		site, err := c.Store.Site(ctx, req.Site)
		if err != nil {
			return err
		}
		s = store.AgentSession{
			ID: req.Site + "-" + now.Format("20060102-150405.000000"), Site: req.Site, OpenedAt: now, LastActive: now,
			Agent: req.Agent, Source: req.Source, WriteGrant: req.WriteGrant,
			LaunchedTool: req.LaunchedTool, AgentRunning: req.LaunchedTool != "",
		}
		// The files are cloned now (fast); a database dump can take long,
		// and agents give an MCP server seconds to start: it runs in a
		// detached process when Covo can start one.
		later := site.Database != "" && c.Detach != nil
		who := req.Agent
		if who == "" {
			who = "an AI agent"
		}
		base, err := c.snapshot(ctx, snapOpts{site: req.Site, kind: SnapshotSession, session: s.ID, filesOnly: later,
			note: "start of the agent session (" + who + ")"})
		if err != nil {
			return fmt.Errorf("the snapshot at the start of the agent session failed: %w", err)
		}
		s.BaseSnapshot, s.DBSaved = base.ID, !later
		c.recordDBStats(ctx, site, base)
		if err := c.Store.AddAgentSession(ctx, s); err != nil {
			_ = c.removeSnapshot(ctx, base)
			return err
		}
		opened = true
		if later {
			pid, err := c.Detach("agent", "session", "save-db", s.ID)
			if err != nil {
				return c.saveSessionDB(ctx, s)
			}
			s.DBPid = pid
			return c.Store.UpdateAgentSession(ctx, s)
		}
		return nil
	})
	if err == nil && req.Wait {
		err = c.WaitSessionDB(ctx, req.Site)
	}
	return s, opened, err
}

// SaveSessionDB writes the database of a session's starting snapshot (run
// by the detached process EnsureAgentSession starts).
func (c *Core) SaveSessionDB(ctx context.Context, id string) error {
	s, err := c.Store.AgentSession(ctx, id)
	if err != nil {
		return err
	}
	return c.saveSessionDB(ctx, s)
}

func (c *Core) saveSessionDB(ctx context.Context, s store.AgentSession) error {
	if s.DBSaved {
		return nil
	}
	site, err := c.Store.Site(ctx, s.Site)
	if err != nil {
		return err
	}
	sn, err := c.Store.Snapshot(ctx, s.Site, s.BaseSnapshot)
	if err != nil {
		return err
	}
	if site.Database != "" && sn.DBFile == "" {
		if err := c.dumpSnapshotDB(ctx, site, &sn); err != nil {
			return fmt.Errorf("save the database at the start of the agent session: %w", err)
		}
		if err := c.Store.UpdateSnapshotDB(ctx, sn); err != nil {
			return err
		}
	}
	cur, err := c.Store.AgentSession(ctx, s.ID)
	if err != nil {
		return err
	}
	cur.DBSaved, cur.DBPid = true, 0
	return c.Store.UpdateAgentSession(ctx, cur)
}

// WaitSessionDB waits until the starting database dump of the site's open
// session is written (nothing to wait for without a session). If the
// process writing it is gone, the dump is written here.
func (c *Core) WaitSessionDB(ctx context.Context, site string) error {
	deadline := time.Now().Add(sessionDBWait)
	for {
		s, err := c.Store.OpenAgentSession(ctx, site)
		if errcode.Code(err) == errcode.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		if s.DBSaved {
			return nil
		}
		if s.DBPid == 0 || unix.Kill(s.DBPid, 0) != nil {
			return c.saveSessionDB(ctx, s)
		}
		if time.Now().After(deadline) {
			return errcode.Errorf(errcode.Service, "the database at the start of the agent session is still being saved")
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for the session database: %w", ctx.Err())
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// TouchAgentSession marks the site's open session as active (no-op
// without one).
func (c *Core) TouchAgentSession(ctx context.Context, site string) {
	if s, err := c.Store.OpenAgentSession(ctx, site); err == nil {
		_ = c.Store.TouchAgentSession(ctx, s.ID, time.Now())
	}
}

// Checkpoint saves the site inside its open session before a risky
// change. Only the newest checkpoints of a session are kept.
func (c *Core) Checkpoint(ctx context.Context, site, note string) (store.Snapshot, error) {
	s, err := c.Store.OpenAgentSession(ctx, site)
	if err != nil {
		return store.Snapshot{}, err
	}
	if err := c.WaitSessionDB(ctx, site); err != nil {
		return store.Snapshot{}, err
	}
	sn, err := c.snapshot(ctx, snapOpts{site: site, kind: SnapshotCheckpoint, session: s.ID, note: note})
	if err != nil {
		return sn, err
	}
	cps, _ := c.sessionCheckpoints(ctx, s)
	for _, old := range cps[min(len(cps), maxSessionCheckpoints):] {
		_ = c.removeSnapshot(ctx, old)
	}
	return sn, nil
}

func (c *Core) sessionCheckpoints(ctx context.Context, s store.AgentSession) ([]store.Snapshot, error) {
	all, err := c.Store.ListSnapshots(ctx, s.Site)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(all, func(sn store.Snapshot) bool {
		return sn.SessionID != s.ID || sn.Kind != SnapshotCheckpoint
	}), nil
}

// AgentSessions returns the open sessions, then up to limit recently
// closed ones when all is set.
func (c *Core) AgentSessions(ctx context.Context, all bool) ([]SessionInfo, error) {
	_, _ = c.ExpireAgentSessions(ctx)
	list, err := c.Store.AgentSessions(ctx, !all, 50)
	if err != nil {
		return nil, err
	}
	out := make([]SessionInfo, 0, len(list))
	for _, s := range list {
		out = append(out, c.sessionInfo(ctx, s))
	}
	return out, nil
}

// AgentSession returns the site's open session with its snapshots.
func (c *Core) AgentSession(ctx context.Context, site string) (SessionInfo, error) {
	if _, err := c.Store.Site(ctx, site); err != nil {
		return SessionInfo{}, err
	}
	_, _ = c.ExpireAgentSessions(ctx)
	s, err := c.Store.OpenAgentSession(ctx, site)
	if err != nil {
		return SessionInfo{}, err
	}
	return c.sessionInfo(ctx, s), nil
}

func (c *Core) sessionInfo(ctx context.Context, s store.AgentSession) SessionInfo {
	info := SessionInfo{AgentSession: s, Checkpoints: []store.Snapshot{}}
	info.IdleHours, _ = c.SessionIdleHours(ctx)
	if site, err := c.Store.Site(ctx, s.Site); err == nil {
		if port, _, err := c.httpsPort(ctx); err == nil {
			info.URL = siteURL(site, port)
		}
	}
	if sn, err := c.Store.Snapshot(ctx, s.Site, s.BaseSnapshot); err == nil {
		info.Base = &sn
	}
	info.Checkpoints, _ = c.sessionCheckpoints(ctx, s)
	return info
}

// CloseResult is a closed session and, after an undo, the restore.
type CloseResult struct {
	Session  store.AgentSession `json:"session"`
	Restored *RestoreResult     `json:"restored,omitempty"`
}

// CloseAgentSession ends the site's open session. keep (and timeout) keep
// the changes; revert puts files and database back to the session's start
// (after saving the current state; .git, node_modules and generated folders are never
// touched). The checkpoints go, the starting snapshot stays as an ordinary
// snapshot, and a temporary write permission ends.
func (c *Core) CloseAgentSession(ctx context.Context, site, outcome string) (CloseResult, error) {
	var res CloseResult
	if !slices.Contains([]string{SessionKeep, SessionRevert, SessionTimeout}, outcome) {
		return res, errcode.Errorf(errcode.InvalidInput, "outcome must be keep or revert")
	}
	s, err := c.Store.OpenAgentSession(ctx, site)
	if err != nil {
		return res, err
	}
	if outcome == SessionRevert {
		if err := c.WaitSessionDB(ctx, site); err != nil {
			return res, err
		}
	}
	err = c.locked(ctx, func() error {
		if outcome == SessionRevert {
			// The agent loses its write permission before the restore, so
			// nothing it does can slip in after it.
			if s.WriteGrant {
				s.WriteGrant = false
				if err := c.Store.UpdateAgentSession(ctx, s); err != nil {
					return err
				}
			}
			r, err := c.restoreSnapshot(ctx, site, s.BaseSnapshot)
			if err != nil {
				return fmt.Errorf("undo the agent session: %w", err)
			}
			res.Restored = &r
		}
		cps, _ := c.sessionCheckpoints(ctx, s)
		for _, cp := range cps {
			_ = c.removeSnapshot(ctx, cp)
		}
		now := time.Now()
		s.ClosedAt, s.Outcome, s.WriteGrant, s.AgentRunning = &now, outcome, false, false
		return c.Store.UpdateAgentSession(ctx, s)
	})
	if err != nil {
		return res, err
	}
	c.pruneSnapshots(ctx, site, "")
	_ = os.RemoveAll(c.agentWorkDir(s.ID))
	res.Session = s
	return res, nil
}

// ExpireAgentSessions closes, keeping the changes, the sessions idle for
// longer than the setting (not while an agent Covo started still runs).
// It returns the sites closed.
func (c *Core) ExpireAgentSessions(ctx context.Context) ([]string, error) {
	h, err := c.SessionIdleHours(ctx)
	if err != nil || h == 0 {
		return nil, err
	}
	open, err := c.Store.AgentSessions(ctx, true, 1000)
	if err != nil {
		return nil, err
	}
	var closed []string
	for _, s := range open {
		if s.AgentRunning || time.Since(s.LastActive) < time.Duration(h)*time.Hour {
			continue
		}
		if _, err := c.CloseAgentSession(ctx, s.Site, SessionTimeout); err == nil {
			closed = append(closed, s.Site)
		}
	}
	return closed, nil
}

// SiteForFolder returns the site whose project folder contains dir ("" if
// none): an agent started there works on that site.
func (c *Core) SiteForFolder(ctx context.Context, dir string) (string, error) {
	dir = filepath.Clean(dir)
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return "", err
	}
	best := ""
	bestLen := 0
	for _, s := range sites {
		if s.Archived {
			continue
		}
		root := filepath.Clean(projectRoot(s))
		if r, err := filepath.EvalSymlinks(root); err == nil {
			root = r
		}
		if (dir == root || strings.HasPrefix(dir, root+string(os.PathSeparator))) && len(root) > bestLen {
			best, bestLen = s.Name, len(root)
		}
	}
	return best, nil
}

// RiskyAgentCall reports whether an agent's call deserves a checkpoint
// inside its session: changes that are hard to undo by hand (database
// imports and migrations, plugin and core updates, URL rewrites).
func RiskyAgentCall(tool string, args []string) bool {
	words := make([]string, 0, len(args))
	flags := []string{}
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
		} else {
			words = append(words, strings.ToLower(a))
		}
	}
	at := func(i int) string {
		if i < len(words) {
			return words[i]
		}
		return ""
	}
	in := func(v string, set ...string) bool { return slices.Contains(set, v) }
	switch tool {
	case ToolWP:
		switch at(0) {
		case "plugin", "theme":
			return in(at(1), "install", "update", "delete", "uninstall")
		case "core":
			return in(at(1), "update", "download", "update-db")
		case "language":
			return in(at(2), "install", "update", "uninstall")
		case "db":
			return in(at(1), "import", "reset", "query", "clean", "drop")
		case "search-replace":
			return !slices.Contains(flags, "--dry-run")
		case "option":
			return in(at(1), "update", "delete") && in(at(2), "siteurl", "home")
		}
	case ToolArtisan:
		cmd := at(0)
		switch {
		case strings.HasPrefix(cmd, "migrate"):
			return cmd != "migrate:status"
		case in(cmd, "db:wipe", "db:seed"):
			return true
		case cmd == "schema:dump":
			return slices.Contains(flags, "--prune")
		}
	case ToolComposer:
		return in(at(0), "update", "upgrade", "require", "remove", "install")
	}
	return false
}
