// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/benbowdev/benbow/internal/store"
)

// pruneOldVersions deletes the builds an update left behind: for each
// pinned component every version but the one in use and the previous one
// (kept for benbow update rollback); for PHP, every patch of a minor but the
// two newest, with their CLI and Xdebug builds. It returns the bytes freed.
// Best effort: a folder it cannot delete stays for the next time.
func (c *Core) pruneOldVersions(ctx context.Context) int64 {
	var freed int64
	remove := func(dir string) {
		if dir == "" || dir == c.Installer.BinDir || !within(dir, c.Installer.BinDir) {
			return
		}
		if _, err := os.Lstat(dir); err != nil {
			return
		}
		n := dirSize(dir)
		if os.RemoveAll(dir) == nil {
			freed += n
		}
	}
	for _, comp := range pinnedComponents {
		cur, ok, err := c.Store.Setting(ctx, settingPin(comp))
		if err != nil || !ok || cur == "" {
			continue // never installed, or no pin to go by
		}
		prev, _, _ := c.Store.Setting(ctx, settingPrevious(comp))
		entries, err := os.ReadDir(filepath.Join(c.Installer.BinDir, comp))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || e.Name() == cur || e.Name() == prev || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			remove(filepath.Join(c.Installer.BinDir, comp, e.Name()))
		}
	}

	installed, err := c.Store.ListPHP(ctx)
	if err != nil {
		return freed
	}
	byMinor := map[string][]store.PHPVersion{}
	for _, v := range installed {
		byMinor[v.Minor] = append(byMinor[v.Minor], v)
	}
	for _, versions := range byMinor {
		if len(versions) <= 2 {
			continue
		}
		slices.SortFunc(versions, func(a, b store.PHPVersion) int { return store.CompareVersions(b.Version, a.Version) })
		for _, v := range versions[2:] {
			if c.Store.RemovePHP(ctx, v.Version) != nil {
				continue
			}
			remove(v.Path)
			for _, comp := range []string{"php-cli", "xdebug"} {
				if art, err := c.Manifest.ResolveFor(comp, v.Version); err == nil {
					remove(c.Installer.Dir(art))
				}
			}
		}
	}
	return freed
}
