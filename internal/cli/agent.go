// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/agents"
	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) agentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "AI agents working on your sites: sessions, keep or undo",
		Long: "While an AI agent works on a site, Covo keeps the site's starting state (files and\n" +
			"database) in an agent session, until you keep the changes or undo them all.",
	}
	cmd.AddCommand(a.agentSessionCmd(), a.agentIdleCmd(), a.agentDiffCmd(), a.agentToolsCmd(), a.agentConnectCmd(), a.agentStartCmd(), a.agentEndedCmd())
	return cmd
}

type sessionListResult []core.SessionInfo

func (r sessionListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No open agent sessions.")
	}
	for _, s := range r {
		who := s.Agent
		if who == "" {
			who = "an agent"
		}
		state := "open"
		if s.ClosedAt != nil {
			state = s.Outcome + " " + s.ClosedAt.Local().Format("2006-01-02 15:04")
		}
		fmt.Fprintf(w, "%-20s %-14s since %s  %d checkpoint(s)  %s\n", s.Site, who, s.OpenedAt.Local().Format("2006-01-02 15:04"), len(s.Checkpoints), state)
	}
}

type sessionResult core.SessionInfo

func (r sessionResult) Text(w io.Writer) {
	who := r.Agent
	if who == "" {
		who = "an AI agent"
	}
	fmt.Fprintf(w, "%s: session opened %s by %s (%s), last active %s\n", r.Site, r.OpenedAt.Local().Format("2006-01-02 15:04"),
		who, r.Source, r.LastActive.Local().Format("2006-01-02 15:04"))
	if r.Base != nil {
		db := ""
		if !r.DBSaved {
			db = " (database still being saved)"
		}
		fmt.Fprintf(w, "Starting point: snapshot %s%s\n", r.Base.ID, db)
	}
	for _, cp := range r.Checkpoints {
		fmt.Fprintf(w, "  checkpoint %s  %s\n", cp.ID, cp.Note)
	}
	fmt.Fprintf(w, "Keep the changes: covo agent session keep %s   Undo everything: covo agent session revert %s\n", r.Site, r.Site)
}

type closeResult core.CloseResult

func (r closeResult) Text(w io.Writer) {
	if r.Restored != nil {
		fmt.Fprintf(w, "Undid the agent session on %s: files and database are back to snapshot %s. The state before the undo is snapshot %s.\n",
			r.Session.Site, r.Restored.Restored.ID, r.Restored.Before.ID)
		return
	}
	fmt.Fprintf(w, "Kept the changes on %s. The starting point stays as snapshot %s for now.\n", r.Session.Site, r.Session.BaseSnapshot)
}

func (a *app) agentSessionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Agent sessions: list, show, keep or undo",
	}
	var all bool
	list := &cobra.Command{
		Use:   "list",
		Short: "Open agent sessions (--all: recently closed ones too)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			l, err := c.AgentSessions(ctx, all)
			return sessionListResult(l), err
		}),
	}
	list.Flags().BoolVar(&all, "all", false, "also recently closed sessions")
	var yes bool
	revert := &cobra.Command{
		Use:   "revert <site>",
		Short: "Undo everything since the session started (files and database; .git untouched)",
		Long: "Puts the site's files and database back to the session's starting snapshot. This also undoes\n" +
			"changes you made yourself during the session: Covo does not know who changed what. .git and\n" +
			"node_modules are never touched, and the current state is saved first (a pre-restore snapshot).",
		Args: cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !yes && !a.confirm("Undo every change to "+args[0]+" since the agent session started, yours included? The current state is saved first.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "undoing a session needs --yes")
			}
			r, err := c.CloseAgentSession(ctx, args[0], core.SessionRevert)
			return closeResult(r), err
		}),
	}
	revert.Flags().BoolVar(&yes, "yes", false, "do not ask")
	saveDB := &cobra.Command{
		Use:    "save-db <id>",
		Short:  "Save a session's starting database (run by Covo in the background)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			return okResult{}, c.SaveSessionDB(ctx, args[0])
		}),
	}
	cmd.AddCommand(list, &cobra.Command{
		Use:   "open <site>",
		Short: "Open a session yourself before an agent (any agent, MCP or not) works on a site",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if _, _, err := c.EnsureAgentSession(ctx, core.SessionRequest{Site: args[0], Source: core.SessionFromUser, Wait: true}); err != nil {
				return nil, err
			}
			s, err := c.AgentSession(ctx, args[0])
			return sessionResult(s), err
		}),
	}, &cobra.Command{
		Use:   "show <site>",
		Short: "A site's open session and its snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			s, err := c.AgentSession(ctx, args[0])
			return sessionResult(s), err
		}),
	}, &cobra.Command{
		Use:   "keep <site>",
		Short: "Keep the changes and close the session",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			r, err := c.CloseAgentSession(ctx, args[0], core.SessionKeep)
			return closeResult(r), err
		}),
	}, revert, saveDB)
	return cmd
}

type idleResult struct {
	Hours int `json:"hours"`
}

func (r idleResult) Text(w io.Writer) {
	if r.Hours == 0 {
		fmt.Fprintln(w, "Agent sessions stay open until you keep or undo them.")
		return
	}
	fmt.Fprintf(w, "An agent session idle for %d hours closes by itself, keeping the changes.\n", r.Hours)
}

func (a *app) agentIdleCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "idle [hours]",
		Short: "After how many idle hours a session closes keeping the changes (0: never)",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if len(args) == 1 {
				h, err := strconv.Atoi(args[0])
				if err != nil {
					return nil, errcode.Errorf(errcode.InvalidInput, "hours must be a number")
				}
				if err := c.SetSessionIdleHours(ctx, h); err != nil {
					return nil, err
				}
			}
			h, err := c.SessionIdleHours(ctx)
			return idleResult{Hours: h}, err
		}),
	}
}

type okResult struct{}

func (okResult) Text(w io.Writer) { fmt.Fprintln(w, "Done.") }

type diffResult core.SessionDiff

func (r diffResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Since %s: %d file(s) added, %d modified, %d deleted\n", r.Since.Local().Format("2006-01-02 15:04"),
		r.Files.Added, r.Files.Modified, r.Files.Deleted)
	for _, f := range r.Files.Changes {
		fmt.Fprintf(w, "  %-8s %s\n", f.Change, f.Path)
	}
	if r.Files.Truncated {
		fmt.Fprintln(w, "  … (list cut)")
	}
	if d := r.Database; d != nil {
		approx := ""
		if d.Approximate {
			approx = " (approximate)"
		}
		fmt.Fprintf(w, "Database %s: %d table(s) changed%s\n", d.Database, len(d.Tables), approx)
		for _, t := range d.Tables {
			fmt.Fprintf(w, "  %-30s %-8s +%d ~%d -%d\n", t.Table, t.Change, t.Inserted, t.Updated, t.Deleted)
		}
		if d.Note != "" {
			fmt.Fprintln(w, "  "+d.Note)
		}
	}
}

type fileDiffResult core.FileDiffResult

func (r fileDiffResult) Text(w io.Writer) {
	switch {
	case r.Binary:
		fmt.Fprintf(w, "%s: binary file %s\n", r.Path, r.Change)
	case r.Diff == "":
		fmt.Fprintf(w, "%s: unchanged\n", r.Path)
	default:
		fmt.Fprint(w, r.Diff)
	}
}

func (a *app) agentDiffCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "diff <site>",
		Short: "What changed since the agent session started: files and database tables",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if file != "" {
				r, err := c.AgentSessionFileDiff(ctx, args[0], file, core.DiffOptions{})
				return fileDiffResult(r), err
			}
			r, err := c.AgentSessionDiff(ctx, args[0])
			return diffResult(r), err
		}),
	}
	cmd.Flags().StringVar(&file, "file", "", "one file's unified diff (path inside the project)")
	return cmd
}

type toolsResult core.AgentTools

func (r toolsResult) Text(w io.Writer) {
	for _, t := range r.Agents {
		state := "not installed"
		if t.Installed {
			state = "installed"
			switch {
			case t.Connected:
				state += ", Covo connected"
			case t.PerRun:
				state += ", Covo connects itself when started from Covo"
			case t.CanConnect:
				state += ", not connected (covo agent connect " + t.ID + ")"
			}
		}
		last := ""
		if t.ID == r.Last {
			last = "  (last used)"
		}
		fmt.Fprintf(w, "%-15s %-26s %s%s\n", t.ID, t.Name, state, last)
	}
}

func (a *app) agentToolsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "tools",
		Short: "AI agents on this Mac and whether Covo is connected to them",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			r, err := c.ListAgentTools(ctx)
			return toolsResult(r), err
		}),
	}
}

type connectResult struct{ agents.ConnectResult }

func (r connectResult) Text(w io.Writer) {
	switch {
	case r.Already:
		fmt.Fprintf(w, "Covo is already in %s.\n", r.ConfigPath)
		return
	case r.Command != "":
		fmt.Fprintf(w, "Ran: %s\n", r.Command)
	default:
		fmt.Fprintf(w, "Added Covo to %s.\n", r.ConfigPath)
	}
	if r.Backup != "" {
		fmt.Fprintf(w, "Backup of the previous file: %s\n", r.Backup)
	}
	if r.Restart {
		fmt.Fprintln(w, "Restart the app to see Covo.")
	}
}

func (a *app) agentConnectCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "connect <agent>",
		Short: "Add Covo's MCP server to an agent's own settings in your home (after a backup)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !yes && !a.confirm("Add Covo to "+args[0]+"'s settings in your home folder? The file is backed up first.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "connecting needs --yes")
			}
			r, err := c.ConnectAgent(ctx, args[0])
			return connectResult{r}, err
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask")
	return cmd
}

type agentStartResult core.StartAgentResult

func (r agentStartResult) Text(w io.Writer) {
	where := "opened " + r.Session.Site + "'s folder"
	if r.Started.Terminal != "" {
		where = "started in a new " + r.Started.Terminal + " window"
	}
	fmt.Fprintf(w, "%s %s. Agent session open since %s (starting snapshot %s); the agent may change %s until it closes.\n",
		r.Agent.Name, where, r.Session.OpenedAt.Local().Format("15:04"), r.Session.BaseSnapshot, r.Session.Site)
	fmt.Fprintf(w, "When done: covo agent diff %s, then covo agent session keep|revert %s\n", r.Session.Site, r.Session.Site)
}

func (a *app) agentStartCmd() *cobra.Command {
	var req core.StartAgentRequest
	var yes bool
	cmd := &cobra.Command{
		Use:   "start <site>",
		Short: "Start your AI agent in a site's folder, inside an agent session",
		Long: "Opens an agent session (starting snapshot of files and database), lets agents change\n" +
			"this site until the session closes, and starts the agent in a new terminal window with\n" +
			"Covo connected for that run (Cursor: opens the folder). Agents: " + strings.Join([]string{"claude", "codex", "gemini", "cursor-agent", "cursor", "copilot"}, ", ") + ".",
		Args: cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req.Site = args[0]
			req.Connect = req.Connect || yes
			r, err := c.StartAgent(ctx, req)
			if errcode.Code(err) == errcode.ConfirmationRequired && !a.json && a.confirm(err.Error()+"\nAdd it?") {
				req.Connect = true
				r, err = c.StartAgent(ctx, req)
			}
			return agentStartResult(r), err
		}),
	}
	cmd.Flags().StringVar(&req.Tool, "tool", "", "claude, codex, gemini, cursor-agent, cursor or copilot (default: the last one)")
	cmd.Flags().StringVar(&req.Terminal, "terminal", "", "Terminal, iTerm or Ghostty (default: the last one, or Terminal)")
	cmd.Flags().BoolVar(&req.Connect, "connect", false, "add Covo to the agent's own settings if it needs it (Gemini, Cursor)")
	cmd.Flags().BoolVar(&yes, "yes", false, "same as --connect")
	return cmd
}

func (a *app) agentEndedCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "ended <session>",
		Short:  "Record that the agent Covo started has exited (run by its window)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			return okResult{}, c.AgentEnded(ctx, args[0])
		}),
	}
}
