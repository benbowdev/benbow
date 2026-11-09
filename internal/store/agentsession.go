// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
)

// AgentSession is the time an AI agent works on a site: it starts with a
// snapshot that stays until the user keeps or undoes the changes.
type AgentSession struct {
	ID         string    `json:"id"`
	Site       string    `json:"site"`
	OpenedAt   time.Time `json:"opened_at"`
	LastActive time.Time `json:"last_active"`
	// Agent is the MCP client's name (e.g. claude-code), when known.
	Agent string `json:"agent,omitempty"`
	// Source is how it opened: mcp-folder, mcp-call or launch.
	Source       string `json:"source"`
	BaseSnapshot string `json:"base_snapshot"`
	// DBSaved is false while the starting database dump is being written.
	DBSaved bool `json:"database_saved"`
	DBPid   int  `json:"-"`
	// WriteGrant lets agents change the site while the session is open.
	WriteGrant   bool       `json:"write_grant,omitempty"`
	LaunchedTool string     `json:"launched_tool,omitempty"`
	AgentRunning bool       `json:"agent_running,omitempty"`
	ClosedAt     *time.Time `json:"closed_at,omitempty"`
	// Outcome is keep, revert or timeout once closed.
	Outcome string `json:"outcome,omitempty"`
}

const sessionCols = `id, site, opened_at, last_active, agent, source, base_snapshot, db_saved, db_pid, write_grant, launched_tool, agent_running, closed_at, outcome`

func scanSession(sc interface{ Scan(...any) error }) (AgentSession, error) {
	var a AgentSession
	var opened, active, closed string
	err := sc.Scan(&a.ID, &a.Site, &opened, &active, &a.Agent, &a.Source, &a.BaseSnapshot, &a.DBSaved, &a.DBPid,
		&a.WriteGrant, &a.LaunchedTool, &a.AgentRunning, &closed, &a.Outcome)
	a.OpenedAt, _ = time.Parse(time.RFC3339Nano, opened)
	a.LastActive, _ = time.Parse(time.RFC3339Nano, active)
	if closed != "" {
		t, _ := time.Parse(time.RFC3339Nano, closed)
		a.ClosedAt = &t
	}
	return a, err
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// AddAgentSession records a new open session. It fails with a conflict if
// the site already has one.
func (s *Store) AddAgentSession(ctx context.Context, a AgentSession) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO agent_sessions (`+sessionCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
		a.ID, a.Site, ts(a.OpenedAt), ts(a.LastActive), a.Agent, a.Source, a.BaseSnapshot, a.DBSaved, a.DBPid,
		a.WriteGrant, a.LaunchedTool, a.AgentRunning)
	if err != nil {
		return fmt.Errorf("record agent session on %s: %w", a.Site, err)
	}
	return nil
}

// UpdateAgentSession saves every field of a session.
func (s *Store) UpdateAgentSession(ctx context.Context, a AgentSession) error {
	closed := ""
	if a.ClosedAt != nil {
		closed = ts(*a.ClosedAt)
	}
	_, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET last_active = ?, agent = ?, source = ?, base_snapshot = ?, db_saved = ?, db_pid = ?,
		write_grant = ?, launched_tool = ?, agent_running = ?, closed_at = ?, outcome = ? WHERE id = ?`,
		ts(a.LastActive), a.Agent, a.Source, a.BaseSnapshot, a.DBSaved, a.DBPid, a.WriteGrant, a.LaunchedTool, a.AgentRunning,
		closed, a.Outcome, a.ID)
	if err != nil {
		return fmt.Errorf("update agent session %s: %w", a.ID, err)
	}
	return nil
}

// OpenAgentSession returns a site's open session (not_found if none).
func (s *Store) OpenAgentSession(ctx context.Context, site string) (AgentSession, error) {
	a, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM agent_sessions WHERE site = ? AND closed_at = ''`, site))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errcode.Errorf(errcode.NotFound, "no open agent session on "+site)
	}
	if err != nil {
		return a, fmt.Errorf("read agent session: %w", err)
	}
	return a, nil
}

// AgentSession returns one session by id.
func (s *Store) AgentSession(ctx context.Context, id string) (AgentSession, error) {
	a, err := scanSession(s.db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM agent_sessions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return a, errcode.Errorf(errcode.NotFound, "no agent session "+id)
	}
	if err != nil {
		return a, fmt.Errorf("read agent session: %w", err)
	}
	return a, nil
}

// AgentSessions returns sessions, newest first: the open ones only, or
// also the last closed ones (up to limit in all).
func (s *Store) AgentSessions(ctx context.Context, openOnly bool, limit int) ([]AgentSession, error) {
	q := `SELECT ` + sessionCols + ` FROM agent_sessions`
	if openOnly {
		q += ` WHERE closed_at = ''`
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY opened_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	defer rows.Close()
	out := []AgentSession{}
	for rows.Next() {
		a, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan agent session: %w", err)
		}
		out = append(out, a)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list agent sessions: %w", err)
	}
	return out, nil
}

// TouchAgentSession marks a session as active now.
func (s *Store) TouchAgentSession(ctx context.Context, id string, at time.Time) error {
	if _, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET last_active = ? WHERE id = ?`, ts(at), id); err != nil {
		return fmt.Errorf("touch agent session: %w", err)
	}
	return nil
}
