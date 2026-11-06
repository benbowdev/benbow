// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"strings"
	"unicode"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/ports"
	"github.com/getcovo/covo/internal/store"
)

// Sites of kind app (docs/apps.md): a dev server in any language behind
// the site's HTTPS domain. Covo either starts the user's command (AppRun)
// or only proxies to a port where the user runs it (AppProxy). Covo never
// installs or picks a runtime: the command runs with the user's own tools.

// appPortFrom is where Covo looks for a free port for an app: away from the
// ports dev servers use by default (3000, 4321, 5173, 8000, 8080…), so a
// server started by hand never collides with one Covo started.
const (
	appPortFrom = 41000
	appPortSpan = 2000
	// maxCommand keeps a command to something that fits on one line.
	maxCommand = 1000
)

// AppHostLocalhost sends Host: localhost:<port> to the app.
const AppHostLocalhost = "localhost"

// checkCommand validates an app's command: one line, no NUL.
func checkCommand(cmd string) error {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return errcode.Errorf(errcode.InvalidInput, "the command is empty")
	}
	if len(cmd) > maxCommand || strings.ContainsFunc(cmd, func(r rune) bool { return r == 0 || r == '\n' || r == '\r' }) {
		return errcode.Newf(errcode.InvalidInput, "a command is one line of at most %d characters", maxCommand)
	}
	if !strings.ContainsFunc(cmd, unicode.IsLetter) {
		return errcode.Errorf(errcode.InvalidInput, "the command does not name a program")
	}
	return nil
}

func checkAppPortRange(port int, what string) error {
	if port < 1024 || port > 65535 {
		return errcode.Newf(errcode.InvalidInput, "%s must be between 1024 and 65535", what)
	}
	return nil
}

// checkAppHost validates the Host header choice.
func checkAppHost(h string) error {
	if h != "" && h != AppHostLocalhost {
		return errcode.Newf(errcode.InvalidInput, "host header %q: use %q or leave it empty for the site's domain", h, AppHostLocalhost)
	}
	return nil
}

// usedAppPorts lists the ports other sites' apps and companions use.
func usedAppPorts(sites []store.Site, except string) map[int]bool {
	used := map[int]bool{}
	for _, s := range sites {
		if s.Name == except {
			continue
		}
		if s.IsApp() && s.AppPort != 0 {
			used[s.AppPort] = true
		}
		if s.CompanionPort != 0 {
			used[s.CompanionPort] = true
		}
	}
	return used
}

// freeAppPort picks a port for an app Covo starts: free now and not given
// to another site. Callers hold the lock.
func (c *Core) freeAppPort(ctx context.Context, except string) (int, error) {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return 0, err
	}
	used := usedAppPorts(sites, except)
	for p := appPortFrom; p < appPortFrom+appPortSpan; p++ {
		if !used[p] && ports.Free(p) {
			return p, nil
		}
	}
	return 0, errcode.Newf(errcode.PortInUse, "no free port for the app between %d and %d", appPortFrom, appPortFrom+appPortSpan-1)
}

// checkAppPortTaken refuses a fixed port another site already uses.
func (c *Core) checkAppPortTaken(ctx context.Context, port int, self string) error {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return err
	}
	if usedAppPorts(sites, self)[port] {
		return errcode.Newf(errcode.Conflict, "port %d is already used by another Covo site", port)
	}
	return nil
}

// phpOnly refuses PHP tools (wp-cli, Composer, PHP settings, the PHP
// compatibility check…) on an app site.
func phpOnly(site store.Site, what string) error {
	if site.IsApp() {
		return errcode.Newf(errcode.InvalidInput, "%s is an app, not a PHP site: %s is only for PHP sites", site.Name, what)
	}
	return nil
}

func (r AddSiteRequest) isApp() bool { return r.Command != "" || r.ProxyPort != 0 }

// checkApp validates the app part of a request without touching state.
func (r AddSiteRequest) checkApp() error {
	switch {
	case r.Command != "" && r.ProxyPort != 0:
		return errcode.Errorf(errcode.InvalidInput, "choose a command for Covo to start or a port to proxy to, not both")
	case r.ProxyPort != 0 && r.Port != 0:
		return errcode.Errorf(errcode.InvalidInput, "--port is for apps Covo starts; a proxied app uses --proxy-port")
	case r.CreateFolder:
		return errcode.Errorf(errcode.InvalidInput, "an app needs its project folder: Covo does not create projects for other languages")
	}
	if r.Command != "" {
		if err := checkCommand(r.Command); err != nil {
			return err
		}
	}
	if r.ProxyPort != 0 {
		if err := checkAppPortRange(r.ProxyPort, "the proxied port"); err != nil {
			return err
		}
	}
	if r.Port != 0 {
		if err := checkAppPortRange(r.Port, "the port"); err != nil {
			return err
		}
	}
	return checkAppHost(r.AppHost)
}

// appFields picks the stored app fields of a checked request: for a
// started app, its fixed port (which must be free) or a free one. Callers
// hold the lock.
func (c *Core) appFields(ctx context.Context, req AddSiteRequest) (store.Site, error) {
	if !req.isApp() {
		return store.Site{}, nil
	}
	s := store.Site{AppHost: req.AppHost}
	if req.ProxyPort != 0 {
		s.AppMode, s.AppPort = store.AppProxy, req.ProxyPort
		return s, c.checkAppPortTaken(ctx, s.AppPort, req.Name)
	}
	s.AppMode, s.Command = store.AppRun, strings.TrimSpace(req.Command)
	port, err := c.runPort(ctx, req.Port, req.Name)
	s.AppPort = port
	return s, err
}

// runPort returns fixed when set (after checking no other site has it and
// nothing listens on it), else a free port.
func (c *Core) runPort(ctx context.Context, fixed int, site string) (int, error) {
	if fixed == 0 {
		return c.freeAppPort(ctx, site)
	}
	if err := c.checkAppPortTaken(ctx, fixed, site); err != nil {
		return 0, err
	}
	if !ports.Free(fixed) {
		return 0, errcode.Newf(errcode.PortInUse, "port %d is in use: %s", fixed, ports.Describe(ports.Owners(ctx, fixed)))
	}
	return fixed, nil
}

// updateAppFields applies an UpdateSiteRequest's app fields to site.
// Callers hold the lock.
func (c *Core) updateAppFields(ctx context.Context, req UpdateSiteRequest, site *store.Site) error {
	if req.Command == "" && req.ProxyPort == 0 && req.Port == 0 && req.AppHost == nil {
		return nil
	}
	if !site.IsApp() {
		return errcode.Newf(errcode.InvalidInput, "%s is a PHP site: to turn a folder into an app, remove the site and add it again with --command or --proxy-port", site.Name)
	}
	switch {
	case req.Command != "" && req.ProxyPort != 0:
		return errcode.Errorf(errcode.InvalidInput, "choose a command for Covo to start or a port to proxy to, not both")
	case req.ProxyPort != 0 && req.Port != 0:
		return errcode.Errorf(errcode.InvalidInput, "--port is for apps Covo starts; a proxied app uses --proxy-port")
	}
	if req.AppHost != nil {
		if err := checkAppHost(*req.AppHost); err != nil {
			return err
		}
		site.AppHost = *req.AppHost
	}
	if req.Command == "" && req.ProxyPort == 0 && req.Port == 0 {
		return nil
	}
	if req.ProxyPort != 0 {
		if err := checkAppPortRange(req.ProxyPort, "the proxied port"); err != nil {
			return err
		}
		if err := c.checkAppPortTaken(ctx, req.ProxyPort, site.Name); err != nil {
			return err
		}
		site.AppMode, site.Command, site.AppPort = store.AppProxy, "", req.ProxyPort
		return nil
	}
	if req.Command != "" {
		if err := checkCommand(req.Command); err != nil {
			return err
		}
		site.Command = strings.TrimSpace(req.Command)
	} else if site.AppMode != store.AppRun && req.Port != 0 {
		return errcode.Errorf(errcode.InvalidInput, "--port is for apps Covo starts: give the command with --command too")
	}
	switch {
	case req.Port != 0 && req.Port != site.AppPort:
		if err := checkAppPortRange(req.Port, "the port"); err != nil {
			return err
		}
		port, err := c.runPort(ctx, req.Port, site.Name)
		if err != nil {
			return err
		}
		site.AppPort = port
	case site.AppMode != store.AppRun:
		// From proxying to starting: the user's server keeps its port.
		port, err := c.freeAppPort(ctx, site.Name)
		if err != nil {
			return err
		}
		site.AppPort = port
	}
	site.AppMode = store.AppRun
	return nil
}
