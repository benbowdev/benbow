// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// siteNameRe is a single DNS label: by default the site is served at
// <name>.localhost.
var siteNameRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// AddSiteRequest is the input of AddSite.
type AddSiteRequest struct {
	Name string
	// Domain defaults to <name>.localhost. It may be any name under
	// .localhost, or .test once enabled (e.g. demo.covo.test).
	Domain string
	PHP    string // minor version, e.g. "8.4"
	Path   string // absolute path of the document root
	// Force accepts a folder without index.php or index.html.
	Force bool
}

// SiteResult is a site with its URL and whether services were (re)applied.
type SiteResult struct {
	SiteStatus
	// Applied is true when Covo was running and the change is already live.
	Applied bool `json:"applied"`
}

// AddSite registers a site, installing its PHP version if needed. If Covo is
// running the site goes live immediately; if that fails the site is removed
// again and the previous configuration restored.
func (c *Core) AddSite(ctx context.Context, req AddSiteRequest) (SiteResult, error) {
	if !siteNameRe.MatchString(req.Name) {
		return SiteResult{}, errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("site name %q must be lowercase letters, digits and dashes", req.Name))
	}
	if req.Domain == "" {
		req.Domain = req.Name + "." + suffixLocalhost
	}
	if _, err := domainSuffix(req.Domain); err != nil {
		return SiteResult{}, err
	}
	if err := validMinor(req.PHP); err != nil {
		return SiteResult{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return SiteResult{}, fmt.Errorf("resolve home directory: %w", err)
	}
	if err := checkSiteDir(req.Path, home, c.Layout.Root); err != nil {
		return SiteResult{}, err
	}
	if !req.Force && !hasIndex(req.Path) {
		return SiteResult{}, errcode.Errorf(errcode.ConfirmationRequired,
			fmt.Sprintf("%s has no index.php or index.html; use --force if it is the right folder", req.Path))
	}

	var res SiteResult
	err = c.locked(ctx, func() error {
		if _, err := c.Store.Site(ctx, req.Name); err == nil {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", req.Name))
		}
		if err := c.checkSiteDomain(ctx, req.Domain); err != nil {
			return err
		}
		if _, err := c.ensurePHP(ctx, req.PHP); err != nil {
			return err
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			return err
		}
		site := store.Site{
			Name:      req.Name,
			Path:      req.Path,
			PHPMinor:  req.PHP,
			Domain:    req.Domain,
			HTTPS:     true,
			CreatedAt: time.Now(),
		}
		if err := c.Store.AddSite(ctx, site); err != nil {
			return err
		}
		res = SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}

		if err := c.syncLocal(ctx); err != nil {
			_ = c.Store.RemoveSite(ctx, site.Name)
			return err
		}
		applied, err := c.applyIfRunning(ctx)
		if err != nil {
			_ = c.Store.RemoveSite(ctx, site.Name)
			_ = c.syncLocal(ctx)
			_, _ = c.applyIfRunning(ctx)
			return err
		}
		res.Applied = applied
		return nil
	})
	return res, err
}

// RemoveSite unregisters a site. Its folder is never touched.
func (c *Core) RemoveSite(ctx context.Context, name string) (SiteResult, error) {
	var res SiteResult
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			return err
		}
		if err := c.Store.RemoveSite(ctx, name); err != nil {
			return err
		}
		res = SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}
		applied, err := c.applyIfRunning(ctx)
		if err != nil {
			_ = c.Store.AddSite(ctx, site)
			_, _ = c.applyIfRunning(ctx)
			return err
		}
		// After Caddy stopped serving it: a leftover hosts line is harmless.
		if err := c.syncLocal(ctx); err != nil {
			return err
		}
		res.Applied = applied
		return nil
	})
	return res, err
}

// ListSites returns all sites with their URLs.
func (c *Core) ListSites(ctx context.Context) ([]SiteStatus, error) {
	st, err := c.Status(ctx)
	if err != nil {
		return nil, err
	}
	return st.Sites, nil
}

// applyIfRunning reconciles services when Covo has been started.
func (c *Core) applyIfRunning(ctx context.Context) (bool, error) {
	_, running, err := c.Store.Setting(ctx, settingRunning)
	if err != nil || !running {
		return false, err
	}
	if _, err := c.reconcile(ctx); err != nil {
		return false, err
	}
	return true, nil
}
