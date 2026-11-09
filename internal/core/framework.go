// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/benbowdev/benbow/internal/store"
)

// Frameworks Benbow recognises in a site's folder.
const (
	FrameworkWordPress = "wordpress"
	FrameworkLaravel   = "laravel"
)

// isLaravelProject reports whether dir is a Laravel project root: artisan
// and laravel/framework required in composer.json.
func isLaravelProject(dir string) bool {
	if fi, err := os.Stat(filepath.Join(dir, "artisan")); err != nil || fi.IsDir() {
		return false
	}
	b, err := os.ReadFile(filepath.Join(dir, "composer.json"))
	if err != nil {
		return false
	}
	var c struct {
		Require map[string]string `json:"require"`
	}
	if json.Unmarshal(b, &c) != nil {
		return false
	}
	_, ok := c.Require["laravel/framework"]
	return ok
}

// detectFramework looks at the folder a user chose for a site and returns
// the document root to serve, the project root (only when it differs) and
// the framework. A Laravel project is served from public/, whether the
// user chose the project or its public folder.
func detectFramework(dir string) (docroot, appRoot, framework string) {
	if isLaravelProject(dir) {
		if fi, err := os.Stat(filepath.Join(dir, "public")); err == nil && fi.IsDir() {
			return filepath.Join(dir, "public"), dir, FrameworkLaravel
		}
	}
	if filepath.Base(dir) == "public" && isLaravelProject(filepath.Dir(dir)) {
		return dir, filepath.Dir(dir), FrameworkLaravel
	}
	if isWordPress(dir) {
		return dir, "", FrameworkWordPress
	}
	return dir, "", ""
}

// projectRoot is where a site's tools run: the Laravel project, else the
// document root.
func projectRoot(s store.Site) string {
	if s.AppRoot != "" {
		return s.AppRoot
	}
	return s.Path
}

// backfillFrameworks recognises the framework of sites added before Benbow
// knew about frameworks (or whose folder changed outside Benbow). Only sites
// without one are looked at; their document root never changes here.
func (c *Core) backfillFrameworks(ctx context.Context) {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return
	}
	for _, s := range sites {
		if s.Framework != "" || s.IsApp() {
			continue
		}
		docroot, appRoot, fw := detectFramework(s.Path)
		if fw == "" || docroot != s.Path {
			continue
		}
		_ = c.Store.SetFramework(ctx, s.Name, fw, appRoot)
	}
}
