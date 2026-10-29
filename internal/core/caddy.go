// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// CaddyInstall is the result of installing Caddy.
type CaddyInstall struct {
	Version string `json:"version"`
	Binary  string `json:"binary"`
	SHA256  string `json:"sha256"`
	Fresh   bool   `json:"fresh"`
}

// InstallCaddy installs the pinned Caddy build. It is idempotent.
func (c *Core) InstallCaddy(ctx context.Context) (CaddyInstall, error) {
	var res CaddyInstall
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.ensureCaddy(ctx)
		return err
	})
	return res, err
}

func (c *Core) ensureCaddy(ctx context.Context) (CaddyInstall, error) {
	art, err := c.Manifest.Resolve("caddy", "")
	if err != nil {
		return CaddyInstall{}, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return CaddyInstall{}, err
	}
	bin := filepath.Join(inst.Dir, "caddy")
	out, err := runQuiet(ctx, bin, "version")
	if err != nil {
		return CaddyInstall{}, errcode.New(errcode.Service, fmt.Errorf("run %s version: %w", bin, err))
	}
	// Output looks like "v2.11.7 h1:…".
	if !strings.HasPrefix(out, "v"+art.Version+" ") {
		return CaddyInstall{}, errcode.Errorf(errcode.Checksum,
			fmt.Sprintf("caddy reports %q, expected v%s", firstLine(out), art.Version))
	}
	return CaddyInstall{Version: art.Version, Binary: bin, SHA256: art.SHA256, Fresh: inst.Fresh}, nil
}
