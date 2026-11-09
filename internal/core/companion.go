// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/ca"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/store"
)

// A PHP site's companion (docs/apps.md) is a dev server started and stopped
// with the site, without a domain of its own: typically Vite next to
// Laravel. It runs like an app's command (login shell, PORT and {port}, its
// own log and state) on a port Benbow gives it.

// laravelVite reports whether the site is Laravel with laravel-vite-plugin,
// which serves HTTPS on the site's domain when given a certificate.
func laravelVite(s store.Site) bool {
	if s.Framework != FrameworkLaravel {
		return false
	}
	root := s.AppRoot
	if root == "" {
		root = s.Path
	}
	return strings.Contains(readSmall(filepath.Join(root, "package.json")), `"laravel-vite-plugin"`)
}

func (c *Core) companionCertPaths(site string) (cert, key string) {
	dir := filepath.Join(c.Layout.Data, "certs")
	return filepath.Join(dir, site+".crt"), filepath.Join(dir, site+".key")
}

// ensureCompanionCert returns a certificate for the site's domain and
// aliases signed by Benbow's CA, reusing the one on disk while it is current.
// The key is readable by the user only.
func (c *Core) ensureCompanionCert(s store.Site) (cert, key string, err error) {
	cert, key = c.companionCertPaths(s.Name)
	names := append([]string{s.Domain}, s.Aliases...)
	now := time.Now()
	if b, err := os.ReadFile(cert); err == nil && ca.LeafCurrent(caDir(c.Layout), b, names, now) {
		if _, err := os.Stat(key); err == nil {
			return cert, key, nil
		}
	}
	chain, keyPEM, err := ca.Leaf(caDir(c.Layout), names, now)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(cert), 0o700); err != nil {
		return "", "", errcode.New(errcode.Internal, err)
	}
	// Key first: a certificate on disk always has its key.
	if _, err := fsutil.WriteAtomic(key, keyPEM, 0o600); err != nil {
		return "", "", err
	}
	if _, err := fsutil.WriteAtomic(cert, chain, 0o600); err != nil {
		return "", "", err
	}
	return cert, key, nil
}

// companionEnvironment is what Benbow adds for a companion: for Laravel with
// laravel-vite-plugin, a certificate for the site's domain and the site's
// address without its port (the plugin listens on APP_URL's host and
// writes public/hot with it), so pages load Vite's scripts over trusted
// HTTPS from the same domain.
func (c *Core) companionEnvironment(_ context.Context, s store.Site) (env, names, warnings []string) {
	if !laravelVite(s) {
		return nil, nil, nil
	}
	cert, key, err := c.ensureCompanionCert(s)
	if err != nil {
		return nil, nil, []string{"no HTTPS certificate for Vite: " + err.Error()}
	}
	env = []string{"VITE_DEV_SERVER_CERT=" + cert, "VITE_DEV_SERVER_KEY=" + key, "APP_URL=https://" + s.Domain}
	return env, []string{"VITE_DEV_SERVER_CERT", "VITE_DEV_SERVER_KEY", "APP_URL"}, nil
}

// updateCompanion applies a companion change to a PHP site: a command
// (with a port from 41000 the first time, or companionPort), or "" to
// remove it. Callers hold the lock.
func (c *Core) updateCompanion(ctx context.Context, req UpdateSiteRequest, site *store.Site) error {
	if req.Companion == nil && req.CompanionPort == 0 {
		return nil
	}
	if site.IsApp() {
		return errcode.Newf(errcode.InvalidInput, "%s is an app: a companion runs next to a PHP site", site.Name)
	}
	if req.Companion != nil {
		cmd := strings.TrimSpace(*req.Companion)
		if cmd == "" {
			if req.CompanionPort != 0 {
				return errcode.Errorf(errcode.InvalidInput, "a companion port without a companion command")
			}
			site.CompanionCommand, site.CompanionPort = "", 0
			return nil
		}
		if err := checkCommand(cmd); err != nil {
			return err
		}
		site.CompanionCommand = cmd
	}
	if site.CompanionCommand == "" {
		return errcode.Newf(errcode.InvalidInput, "%s has no companion: give its command with --companion", site.Name)
	}
	switch {
	case req.CompanionPort != 0 && req.CompanionPort != site.CompanionPort:
		if err := checkAppPortRange(req.CompanionPort, "the companion port"); err != nil {
			return err
		}
		port, err := c.runPort(ctx, req.CompanionPort, site.Name)
		if err != nil {
			return err
		}
		site.CompanionPort = port
	case site.CompanionPort == 0:
		port, err := c.freeAppPort(ctx, site.Name)
		if err != nil {
			return err
		}
		site.CompanionPort = port
	}
	return nil
}
