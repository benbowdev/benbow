// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"

	"github.com/getcovo/covo/internal/caddy"
)

// adminerPHP runs Adminer, whatever the sites use.
const adminerPHP = "8.4"

// adminer installs the pinned Adminer while MySQL is enabled and returns
// how Caddy serves it, or nil.
func (c *Core) adminer(ctx context.Context) (*caddy.Adminer, error) {
	on, err := c.mysqlEnabled(ctx)
	if err != nil || !on {
		return nil, err
	}
	art, err := c.Manifest.Resolve("adminer", "")
	if err != nil {
		return nil, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return nil, err
	}
	return &caddy.Adminer{Dir: inst.Dir, FPMSocket: c.Layout.Socket(socketName(adminerPHP))}, nil
}
