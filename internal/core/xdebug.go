// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

func settingXdebug(minor string) string { return "xdebug_" + minor }

// XdebugStatus is Xdebug's state for one installed PHP version.
type XdebugStatus struct {
	PHP        string `json:"php"`         // minor, e.g. 8.4
	PHPVersion string `json:"php_version"` // exact, e.g. 8.4.23
	Enabled    bool   `json:"enabled"`
	// Available is true when Covo has an xdebug.so built for PHPVersion.
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	Path      string `json:"path,omitempty"`
}

// XdebugResult reports what xdebug on/off did.
type XdebugResult struct {
	XdebugStatus
	Changed bool `json:"changed"`
	// Applied is false when Covo is stopped: PHP-FPM picks it up on start.
	Applied bool `json:"applied"`
}

func (c *Core) xdebugEnabled(ctx context.Context, minor string) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingXdebug(minor))
	return ok, err
}

// ensureXdebug installs the xdebug.so built for exactly v.Version and
// returns its path.
func (c *Core) ensureXdebug(ctx context.Context, v store.PHPVersion) (string, string, error) {
	art, err := c.Manifest.ResolveFor("xdebug", v.Version)
	if errcode.Code(err) == errcode.NotFound {
		return "", "", errcode.Errorf(errcode.Unsupported,
			fmt.Sprintf("Covo has no Xdebug build for PHP %s on %s", v.Version, runtime.GOARCH))
	}
	if err != nil {
		return "", "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(inst.Dir, art.Files[0]), art.Version, nil
}

// xdebugPath returns the xdebug.so php.ini loads for v, or "" when off.
func (c *Core) xdebugPath(ctx context.Context, v store.PHPVersion) (string, error) {
	on, err := c.xdebugEnabled(ctx, v.Minor)
	if err != nil || !on {
		return "", err
	}
	path, _, err := c.ensureXdebug(ctx, v)
	return path, err
}

func (c *Core) xdebugStatus(ctx context.Context, v store.PHPVersion) (XdebugStatus, error) {
	st := XdebugStatus{PHP: v.Minor, PHPVersion: v.Version}
	var err error
	if st.Enabled, err = c.xdebugEnabled(ctx, v.Minor); err != nil {
		return st, err
	}
	if art, err := c.Manifest.ResolveFor("xdebug", v.Version); err == nil {
		st.Available, st.Version = true, art.Version
		if st.Enabled {
			st.Path = filepath.Join(c.Installer.Dir(art), art.Files[0])
		}
	}
	return st, nil
}

// Xdebug reports Xdebug for every installed PHP version.
func (c *Core) Xdebug(ctx context.Context) ([]XdebugStatus, error) {
	versions, err := c.Store.ListPHP(ctx)
	if err != nil {
		return nil, err
	}
	out := []XdebugStatus{}
	seen := map[string]bool{}
	for _, v := range versions {
		if seen[v.Minor] {
			continue
		}
		latest, err := c.Store.LatestPHP(ctx, v.Minor)
		if err != nil {
			return nil, err
		}
		seen[v.Minor] = true
		st, err := c.xdebugStatus(ctx, latest)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, nil
}

// SetXdebug turns Xdebug on or off for a PHP minor version (installing that
// PHP version if needed) and restarts its PHP-FPM if Covo is running.
func (c *Core) SetXdebug(ctx context.Context, minor string, on bool) (XdebugResult, error) {
	var res XdebugResult
	if err := validMinor(minor); err != nil {
		return res, err
	}
	err := c.locked(ctx, func() error {
		v, err := c.ensurePHP(ctx, minor)
		if err != nil {
			return err
		}
		was, err := c.xdebugEnabled(ctx, minor)
		if err != nil {
			return err
		}
		if on {
			// Download and verify now, so that problems show here.
			if _, _, err := c.ensureXdebug(ctx, v); err != nil {
				return err
			}
			err = c.Store.SetSetting(ctx, settingXdebug(minor), "1")
		} else {
			err = c.Store.DeleteSetting(ctx, settingXdebug(minor))
		}
		if err != nil {
			return err
		}
		res.Changed = was != on
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.XdebugStatus, err = c.xdebugStatus(ctx, v)
		return err
	})
	return res, err
}
