// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"

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
	cmd.AddCommand(a.agentSessionCmd(), a.agentIdleCmd())
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
