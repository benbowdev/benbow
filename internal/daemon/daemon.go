// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package daemon runs the core as a launchd user agent serving the local API
// (see package api), and starts, stops and inspects that agent.
//
// Like every Benbow service it is loaded from the Benbow home and does not start
// at login: the app starts it when it opens.
package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/benbowdev/benbow/internal/api"
	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/version"
)

const (
	readyTimeout = 15 * time.Second
	maxLogSize   = 10 << 20
	// exitTimeout leaves time for requests in flight (a PHP download, an
	// import) before launchd kills the daemon.
	exitTimeout = 60
)

// Status is the state of the daemon agent.
type Status struct {
	Label  string `json:"label"`
	Socket string `json:"socket"`
	Loaded bool   `json:"loaded"`
	PID    int    `json:"pid,omitempty"`
	// Answering is true when the API answered on the socket.
	Answering bool `json:"answering"`
	// Version is the daemon's own version, as reported over the API.
	Version *version.Info `json:"version,omitempty"`
}

// Result is a Status with what start or stop did.
type Result struct {
	Status
	Action string `json:"action"` // "started", "restarted", "unchanged", "stopped", "not running"
}

// Run serves the API until SIGTERM or SIGINT. It is the agent's program.
func Run(ctx context.Context) error {
	c, err := core.Open(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()
	if sites, err := c.RecoverRestores(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "interrupted restore: %v\n", err)
	} else if len(sites) > 0 {
		fmt.Fprintf(os.Stderr, "interrupted restore put back: %v\n", sites)
	}
	sock := c.Layout.Socket(api.SocketName)
	ln, err := api.Listen(sock)
	if errcode.Code(err) == errcode.Conflict {
		fmt.Fprintf(os.Stderr, "%v; this one exits\n", err)
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(sock) }()
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	fmt.Fprintf(os.Stderr, "benbow daemon %s listening on %s\n", version.Get().Version, sock)
	go checkUpdatesDaily(ctx, c)
	go expirePhone(ctx, c)
	return api.Serve(ctx, ln, c)
}

// checkUpdatesDaily looks for component updates a few minutes after start
// and then every day. It only downloads the signed manifest: nothing is
// installed without the user (benbow update apply, or the app).
// expirePhone turns "Open on phone" off when its time is up, and closes
// idle agent sessions (keeping the changes).
func expirePhone(ctx context.Context, c *core.Core) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
		if _, err := c.ExpirePhone(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "phone expiry: %v\n", err)
		}
		if closed, err := c.ExpireAgentSessions(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "agent sessions: %v\n", err)
		} else if len(closed) > 0 {
			fmt.Fprintf(os.Stderr, "agent sessions idle, changes kept: %v\n", closed)
		}
	}
}

// checkUpdatesDaily runs the automatic update check: at most once every 24
// hours after the last one that worked (so a Mac counts once a day, see
// docs/updates.md), never when the user turned it off. It looks at the
// wall clock every 30 minutes because Go's timers stop while the Mac
// sleeps; after a failure it waits an hour.
func checkUpdatesDaily(ctx context.Context, c *core.Core) {
	if os.Getenv("BENBOW_NO_UPDATE_CHECK") != "" {
		return
	}
	wait := 3 * time.Minute
	var retryAt time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = 30 * time.Minute
		now := time.Now()
		if now.Before(retryAt) {
			continue
		}
		if due, err := c.AutoCheckDue(ctx, now); err != nil || !due {
			continue
		}
		if _, err := c.AutoCheckUpdates(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "update check: %v\n", err)
			retryAt = now.Add(time.Hour)
		}
	}
}

func agent(c *core.Core, program, hash string) launchd.Agent {
	env := map[string]string{"BENBOW_BINARY_HASH": hash}
	// The daemon must see the same Benbow home, label prefix and overrides as
	// whoever started it (tests rely on this).
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok && strings.HasPrefix(k, "BENBOW_") && k != "BENBOW_BINARY_HASH" {
			env[k] = v
		}
	}
	return launchd.Agent{
		Label:       c.DaemonLabel(),
		Program:     program,
		Args:        []string{"daemon", "run"},
		Env:         env,
		WorkDir:     c.Layout.Root,
		StdoutLog:   filepath.Join(c.Layout.Logs, core.DaemonService+".out.log"),
		StderrLog:   filepath.Join(c.Layout.Logs, core.DaemonService+".err.log"),
		ExitTimeout: exitTimeout,
		// Run exits 0 when another daemon already answers: launchd must
		// not keep trying.
		RestartOnFailure: true,
	}
}

// Start loads the daemon agent running program (the benbow binary), or
// restarts it if program or its contents changed, and waits until the API
// answers.
func Start(ctx context.Context, c *core.Core, program string) (Result, error) {
	program, err := filepath.EvalSymlinks(program)
	if err != nil {
		return Result{}, fmt.Errorf("resolve benbow binary: %w", err)
	}
	hash, err := fileHash(program)
	if err != nil {
		return Result{}, err
	}
	a := agent(c, program, hash)
	for _, p := range []string{a.StdoutLog, a.StderrLog} {
		if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogSize {
			_ = os.Truncate(p, 0)
		}
	}
	lc := launchd.New()
	ch, err := lc.Ensure(ctx, c.Layout.Launchd, a)
	if err != nil {
		return Result{}, err
	}
	res := Result{Action: ch.Action}
	deadline := time.Now().Add(readyTimeout)
	for {
		if res.Status, err = Inspect(ctx, c); err != nil {
			return res, err
		}
		// Ensure waited for any old process to exit: whoever answers now
		// runs program.
		if res.Answering {
			return res, nil
		}
		if time.Now().After(deadline) {
			return res, errcode.Errorf(errcode.Service, "the Benbow daemon did not answer on "+res.Socket+"; see "+a.StderrLog)
		}
		select {
		case <-ctx.Done():
			return res, fmt.Errorf("wait for the daemon: %w", ctx.Err())
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// Stop unloads the daemon agent.
func Stop(ctx context.Context, c *core.Core) (Result, error) {
	lc := launchd.New()
	loaded, err := lc.List(ctx)
	if err != nil {
		return Result{}, err
	}
	res := Result{Action: "not running"}
	if _, ok := loaded[c.DaemonLabel()]; ok {
		if err := lc.Bootout(ctx, c.DaemonLabel()); err != nil {
			return res, err
		}
		res.Action = "stopped"
	}
	_ = os.Remove(filepath.Join(c.Layout.Launchd, c.DaemonLabel()+".plist"))
	res.Status, err = Inspect(ctx, c)
	return res, err
}

// Inspect reports the agent's state and asks the API for its version.
func Inspect(ctx context.Context, c *core.Core) (Status, error) {
	st := Status{Label: c.DaemonLabel(), Socket: c.Layout.Socket(api.SocketName)}
	loaded, err := launchd.New().List(ctx)
	if err != nil {
		return st, err
	}
	if ls, ok := loaded[st.Label]; ok {
		st.Loaded, st.PID = true, ls.PID
	}
	if v, err := ping(ctx, st.Socket); err == nil {
		st.Answering, st.Version = true, &v
	}
	return st, nil
}

func ping(ctx context.Context, sock string) (version.Info, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://benbow/v1/version", nil)
	if err != nil {
		return version.Info{}, fmt.Errorf("ping: %w", err)
	}
	resp, err := api.Client(sock).Do(req)
	if err != nil {
		return version.Info{}, fmt.Errorf("ping: %w", err)
	}
	defer resp.Body.Close()
	var env struct {
		OK   bool         `json:"ok"`
		Data version.Info `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return version.Info{}, fmt.Errorf("ping: %w", err)
	}
	if !env.OK {
		return version.Info{}, errors.New("ping: not ok")
	}
	return env.Data, nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash %s: %w", path, err)
	}
	return hex.EncodeToString(h.Sum(nil)[:8]), nil
}
