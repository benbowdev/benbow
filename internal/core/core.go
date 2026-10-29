// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package core is Covo's only source of truth. The CLI (and later the GUI and
// MCP server, through a local API) are thin clients of this package.
package core

import (
	"context"
	"net/http"
	"time"

	"github.com/getcovo/covo/internal/components"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/lock"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/resolver"
	"github.com/getcovo/covo/internal/store"
)

// lockWait is how long a command waits for another covo command to finish.
const lockWait = 30 * time.Second

// Core holds the resolved environment and open state.
type Core struct {
	Layout    paths.Layout
	Store     *store.Store
	Manifest  components.Manifest
	Installer components.Installer

	launchctl *launchd.Launchctl
	resolver  resolver.Manager
}

// Open prepares the Covo home and opens the state database.
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
	manifest, err := components.Embedded()
	if err != nil {
		_ = st.Close()
		return nil, err
	}
	return &Core{
		Layout:   layout,
		Store:    st,
		Manifest: manifest,
		Installer: components.Installer{
			BinDir: layout.Bin,
			TmpDir: layout.Tmp,
			Client: &http.Client{Timeout: 10 * time.Minute},
		},
		launchctl: launchd.New(),
		resolver:  newResolverManager(layout.Root, layout.Tmp),
	}, nil
}

// Close releases the database.
func (c *Core) Close() error {
	return c.Store.Close()
}

// locked runs fn while holding the global Covo lock.
func (c *Core) locked(ctx context.Context, fn func() error) error {
	ctx, cancel := context.WithTimeout(ctx, lockWait)
	l, err := lock.Acquire(ctx, c.Layout.Lock)
	cancel()
	if err != nil {
		return err
	}
	defer func() { _ = l.Unlock() }()
	return fn()
}
