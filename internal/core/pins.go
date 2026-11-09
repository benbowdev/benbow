// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"

	"github.com/benbowdev/benbow/internal/components"
)

// Components whose version stays fixed until the user updates it (benbow
// update). PHP is per installed version (see php.go); the CA bundle and
// WordPress for new sites always use the newest in the manifest.
var pinnedComponents = []string{"caddy", "mysql", "mailpit", "adminer", "wpcli", "composer", "composer-lts", "adminer-full", "postgres", "redis"}

func settingPin(component string) string      { return "pin_" + component }
func settingPrevious(component string) string { return "previous_" + component }

// pinned returns the artifact of component that Benbow uses: the version
// fixed for it, or (the first time) the newest matching series, which is
// then fixed.
func (c *Core) pinned(ctx context.Context, component, series string) (components.Artifact, error) {
	v, ok, err := c.Store.Setting(ctx, settingPin(component))
	if err != nil {
		return components.Artifact{}, err
	}
	if ok {
		return c.Manifest.Resolve(component, v)
	}
	art, err := c.Manifest.Resolve(component, series)
	if err != nil {
		return art, err
	}
	return art, c.Store.SetSetting(ctx, settingPin(component), art.Version)
}

// pinnedVersion reports the fixed version without fixing one.
func (c *Core) pinnedVersion(ctx context.Context, component, series string) string {
	if v, ok, err := c.Store.Setting(ctx, settingPin(component)); err == nil && ok {
		return v
	}
	if art, err := c.Manifest.Resolve(component, series); err == nil {
		return art.Version
	}
	return ""
}
