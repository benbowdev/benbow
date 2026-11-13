// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package core is Benbow's only source of truth. The CLI (and later the GUI and
// MCP server, through a local API) are thin clients of this package.
package core

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/benbowdev/benbow/internal/components"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/lock"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/resolver"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/benbowdev/benbow/internal/version"
)

// DaemonService is the agent serving the local API (`benbow daemon`). Start,
// stop and reconcile never touch it: the app talks to the core through it.
const DaemonService = "core"

// DaemonLabel returns the daemon's launchd label.
func (c *Core) DaemonLabel() string { return c.labelFor(DaemonService) }

// lockWait is how long a command waits for another benbow command to finish.
const lockWait = 30 * time.Second

// Core holds the resolved environment and open state.
type Core struct {
	Layout    paths.Layout
	Store     *store.Store
	Manifest  components.Manifest
	Installer components.Installer

	launchctl *launchd.Launchctl
	resolver  resolver.Manager

	// manifestStamp is the stored signed manifest's modification time when
	// it was last read: another benbow (the CLI) may accept a newer one.
	manifestStamp time.Time

	// Detach starts `benbow <args>` as a process that outlives this one (an
	// agent session's database dump) and returns its pid. Nil runs the
	// work in-process.
	Detach func(args ...string) (int, error)

	// Executable is the benbow binary the agents of apps run (`benbow app
	// run`); empty when this is not the benbow binary (a test).
	Executable string

	// keychainRun runs /usr/bin/security for the items Core reads itself
	// (an app's variables); nil is the real one. Tests use keychain.Fake.
	keychainRun keychain.Runner

	// adminerLogins are one-time Adminer logins (see adminer.go); they only
	// live in the daemon's memory.
	adminerMu     sync.Mutex
	adminerLogins map[string]adminerLogin
}

// Open prepares the Benbow home and opens the state database.
func Open(ctx context.Context) (*Core, error) {
	layout, err := paths.Default()
	if err != nil {
		return nil, err
	}
	if err := layout.Ensure(); err != nil {
		return nil, err
	}
	st, err := store.Open(ctx, layout.DB)
	if err != nil {
		return nil, err
	}
	lc := launchd.New()
	lc.Keep = []string{lc.Prefix + "." + DaemonService}
	manifest, err := components.Embedded()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	c := &Core{
		Layout:   layout,
		Store:    st,
		Manifest: manifest,
		Installer: components.Installer{
			BinDir: layout.Bin,
			TmpDir: layout.Tmp,
			Client: &http.Client{Timeout: 10 * time.Minute, Transport: version.Transport{}},
		},
		launchctl: lc,
		resolver:  newResolverManager(layout.Root, layout.Tmp),
	}
	c.Detach = detachBenbow(layout)
	c.Executable = benbowExecutable()
	// A newer signed manifest accepted earlier (benbow update check) adds
	// versions; a bad or older one is ignored and the embedded one used.
	c.loadSignedManifest()
	c.backfillFrameworks(ctx)
	c.tidyMigratedInstall()
	return c, nil
}

// Close releases the database.
func (c *Core) Close() error {
	return c.Store.Close()
}

// locked runs fn while holding the global Benbow lock.
func (c *Core) locked(ctx context.Context, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	l, err := lock.Acquire(ctx, c.Layout.Lock)
	cancel()
	if err != nil {
		return err
	}
	defer func() { _ = l.Unlock() }()
	c.reloadSignedManifest()
	return fn()
}

// detachBenbow returns a Detach that runs this benbow binary in its own
// session, so it outlives the process that started it (nil when this is
// not the benbow binary, e.g. a test).
func detachBenbow(layout paths.Layout) func(args ...string) (int, error) {
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "benbow" {
		return nil
	}
	return func(args ...string) (int, error) {
		logf, err := os.OpenFile(filepath.Join(layout.Logs, "background.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return 0, fmt.Errorf("open background log: %w", err)
		}
		defer logf.Close()
		cmd := exec.Command(exe, args...)
		cmd.Stdout, cmd.Stderr = logf, logf
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			return 0, fmt.Errorf("start benbow %s: %w", strings.Join(args, " "), err)
		}
		pid := cmd.Process.Pid
		_ = cmd.Process.Release()
		return pid, nil
	}
}
