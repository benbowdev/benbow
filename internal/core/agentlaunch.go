// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/agents"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// "Work with an agent": Covo starts the agent the user already has in the
// site's folder, inside an agent session (starting snapshot first), with
// permission to change that site for the session only, Covo's MCP server
// connected for that run, and a short context. Covo never installs an
// agent, never calls a model and writes nothing into the project.

const (
	settingAgentLast     = "agent_last_tool"
	settingAgentTerminal = "agent_terminal"
)

// AgentTools lists the agents on this Mac and whether Covo is connected.
type AgentTools struct {
	Agents []agents.Agent `json:"agents"`
	// Last is the agent the user started last.
	Last     string `json:"last,omitempty"`
	Terminal string `json:"terminal,omitempty"`
	// CovoBin is the covo the agents run as their MCP server.
	CovoBin string `json:"covo_bin"`
}

func covoBinary() string {
	exe, err := os.Executable()
	if err != nil {
		return "covo"
	}
	if r, err := filepath.EvalSymlinks(exe); err == nil {
		return r
	}
	return exe
}

// ListAgentTools detects the agents installed on this Mac.
func (c *Core) ListAgentTools(ctx context.Context) (AgentTools, error) {
	res := AgentTools{Agents: agents.Detect(ctx, agents.DefaultEnv()), CovoBin: covoBinary()}
	var err error
	if res.Last, _, err = c.Store.Setting(ctx, settingAgentLast); err != nil {
		return res, err
	}
	res.Terminal, _, err = c.Store.Setting(ctx, settingAgentTerminal)
	return res, err
}

// ConnectAgent adds Covo to an agent's global configuration (in the
// user's home, after a backup in Covo's folder).
func (c *Core) ConnectAgent(ctx context.Context, id string) (agents.ConnectResult, error) {
	res, err := agents.Connect(ctx, agents.DefaultEnv(), id, covoBinary(), filepath.Join(c.Layout.Data, "backups", "agents"))
	if err != nil {
		return res, errcode.New(errcode.InvalidInput, err)
	}
	return res, nil
}

// StartAgentRequest starts an agent on a site.
type StartAgentRequest struct {
	Site string
	Tool string // agents.Claude, … ("" : the last one)
	// Terminal is the terminal app (Terminal, iTerm, Ghostty); "" keeps
	// the last one.
	Terminal string
	// Connect adds Covo to the agent's global configuration when it has
	// no way to get it for one run (Gemini, Cursor): the user agreed.
	Connect bool
}

// StartAgentResult is the started agent and its session.
type StartAgentResult struct {
	Session SessionInfo           `json:"session"`
	Started agents.Started        `json:"started"`
	Connect *agents.ConnectResult `json:"connected,omitempty"`
	Agent   agents.Agent          `json:"agent"`
}

// StartAgent opens the site's agent session (waiting for the starting
// snapshot), lets agents change that site until the session closes, and
// starts the agent in the site's folder.
func (c *Core) StartAgent(ctx context.Context, req StartAgentRequest) (StartAgentResult, error) {
	var res StartAgentResult
	site, err := c.Store.Site(ctx, req.Site)
	if err != nil {
		return res, err
	}
	if req.Tool == "" {
		req.Tool, _, _ = c.Store.Setting(ctx, settingAgentLast)
	}
	if req.Tool == "" {
		return res, errcode.Errorf(errcode.InvalidInput, "choose an agent (covo agent tools lists them)")
	}
	env := agents.DefaultEnv()
	a, err := agents.Find(ctx, env, req.Tool)
	if err != nil {
		return res, errcode.New(errcode.InvalidInput, err)
	}
	if !a.Installed {
		return res, errcode.Errorf(errcode.NotFound, a.Name+" is not installed on this Mac (Covo never installs agents)")
	}
	if a.Kind == "desktop" {
		return res, errcode.Errorf(errcode.Unsupported, a.Name+" does not work in a folder: connect it in Settings → AI Agents")
	}
	if !a.PerRun && !a.Connected {
		if !req.Connect {
			return res, errcode.Newf(errcode.ConfirmationRequired,
				"%s only sees Covo through its own settings (%s): Covo adds itself there, after a backup, if you agree (--connect)", a.Name, a.ConfigPath)

		}
		cr, err := c.ConnectAgent(ctx, a.ID)
		if err != nil {
			return res, err
		}
		res.Connect = &cr
	}
	if req.Terminal == "" {
		req.Terminal, _, _ = c.Store.Setting(ctx, settingAgentTerminal)
	}

	s, _, err := c.EnsureAgentSession(ctx, SessionRequest{Site: site.Name, Agent: a.Name, Source: SessionFromLaunch,
		WriteGrant: true, LaunchedTool: a.ID, Wait: true})
	if err != nil {
		return res, err
	}
	if res.Session, err = c.AgentSession(ctx, site.Name); err != nil {
		return res, err
	}
	l := agents.Launch{
		Dir: projectRoot(site), CovoBin: covoBinary(), Session: s.ID,
		Context: c.agentContext(site, res.Session), WorkDir: c.agentWorkDir(s.ID), Terminal: req.Terminal,
		Prompt: fmt.Sprintf("You are working on the Covo site %q (%s) inside a Covo agent session. Call covo_status first, then wait for my request.",
			site.Name, res.Session.URL),
	}
	if res.Started, err = agents.Start(ctx, a, l); err != nil {
		s.AgentRunning = false
		_ = c.Store.UpdateAgentSession(ctx, s)
		return res, errcode.New(errcode.Service, err)
	}
	res.Agent = a
	_ = c.Store.SetSetting(ctx, settingAgentLast, a.ID)
	if res.Started.Terminal != "" {
		_ = c.Store.SetSetting(ctx, settingAgentTerminal, res.Started.Terminal)
	}
	return res, nil
}

func (c *Core) agentWorkDir(session string) string {
	return filepath.Join(c.Layout.Tmp, "agent", session)
}

// AgentEnded records that the agent Covo started has exited (its window's
// script calls `covo agent ended`); the session stays open.
func (c *Core) AgentEnded(ctx context.Context, id string) error {
	s, err := c.Store.AgentSession(ctx, id)
	if err != nil {
		return err
	}
	s.AgentRunning = false
	s.LastActive = time.Now()
	return c.Store.UpdateAgentSession(ctx, s)
}

// agentContext is what the started agent is told about the site.
func (c *Core) agentContext(site store.Site, s SessionInfo) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Covo session for the site %q\n\n", site.Name)
	fmt.Fprintf(&b, "You are working on a local website managed by Covo, a PHP/WordPress/Laravel development environment on this Mac.\n\n")
	fmt.Fprintf(&b, "- Site: %s, address %s\n", site.Name, s.URL)
	fmt.Fprintf(&b, "- Project folder: %s\n", projectRoot(site))
	if site.IsApp() {
		fmt.Fprintf(&b, "- App (dev server on port %d", site.AppPort)
		if site.Command != "" {
			fmt.Fprintf(&b, ", started by Covo with: %s", site.Command)
		}
		b.WriteString(")")
	} else {
		fmt.Fprintf(&b, "- PHP %s", site.PHPMinor)
	}
	if site.Framework != "" {
		fmt.Fprintf(&b, ", %s", site.Framework)
	}
	b.WriteString("\n")
	if site.Database != "" {
		fmt.Fprintf(&b, "- Database: %s (%s)\n", site.Database, engineOf(site))
	}
	b.WriteString(`
Covo opened an agent session: it saved the site's files and database as they were, and the user will keep your changes or undo them all in Covo. You may change this site through Covo's MCP tools (wp, composer, artisan…) for this session.

Recommended workflow:
1. covo_status, then session_status for this site.
2. Make the changes. Use the wp, artisan and composer tools of Covo (they run with the site's PHP); Covo takes a checkpoint before risky ones.
3. Check your work: fetch_page (page, redirects and the PHP errors of that request), site_logs, mail_messages/read_mail for mail, query_database to read the database.
4. Before saying you are done, call session_diff and show the user what changed (files and database tables), and remind them to keep or undo the changes in Covo.
`)
	return b.String()
}
