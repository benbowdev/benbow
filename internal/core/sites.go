// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"maps"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/caddy"
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
	if err := c.checkSiteRequest(req); err != nil {
		return SiteResult{}, err
	}
	if req.Domain == "" {
		req.Domain = req.Name + "." + suffixLocalhost
	}
	var res SiteResult
	err := c.locked(ctx, func() error {
		site, err := c.insertSite(ctx, req)
		if err != nil {
			return err
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			_ = c.Store.RemoveSite(ctx, site.Name)
			return err
		}
		res = SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}
		res.Applied, err = c.goLive(ctx, []store.Site{site})
		return err
	})
	return res, err
}

// checkSiteRequest validates a request without touching any state.
func (c *Core) checkSiteRequest(req AddSiteRequest) error {
	if !siteNameRe.MatchString(req.Name) {
		return errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("site name %q must be lowercase letters, digits and dashes", req.Name))
	}
	domain := req.Domain
	if domain == "" {
		domain = req.Name + "." + suffixLocalhost
	}
	if _, err := domainSuffix(domain); err != nil {
		return err
	}
	if err := validMinor(req.PHP); err != nil {
		return err
	}
	return c.checkSiteFolder(req.Path, req.Force)
}

// checkSiteFolder checks a document root: allowed, and with an index file
// unless force is set.
func (c *Core) checkSiteFolder(path string, force bool) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolve home directory: %w", err)
	}
	if err := checkSiteDir(path, home, c.Layout.Root); err != nil {
		return err
	}
	if !force && !hasIndex(path) {
		return errcode.Errorf(errcode.ConfirmationRequired,
			fmt.Sprintf("%s has no index.php or index.html; use --force if it is the right folder", path))
	}
	return nil
}

// insertSite stores a checked request, installing its PHP version if
// needed. Callers hold the lock and then call goLive.
func (c *Core) insertSite(ctx context.Context, req AddSiteRequest) (store.Site, error) {
	if _, err := c.Store.Site(ctx, req.Name); err == nil {
		return store.Site{}, errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", req.Name))
	}
	if err := c.checkSiteDomain(ctx, req.Domain); err != nil {
		return store.Site{}, err
	}
	if err := c.checkDomainFree(ctx, req.Domain, ""); err != nil {
		return store.Site{}, err
	}
	if _, err := c.ensurePHP(ctx, req.PHP); err != nil {
		return store.Site{}, err
	}
	site := store.Site{
		Name:        req.Name,
		Path:        req.Path,
		PHPMinor:    req.PHP,
		Domain:      req.Domain,
		HTTPS:       true,
		Enabled:     true,
		Aliases:     []string{},
		PHPSettings: map[string]string{},
		CreatedAt:   time.Now(),
	}
	if err := c.Store.AddSite(ctx, site); err != nil {
		return store.Site{}, err
	}
	return site, nil
}

// goLive syncs /etc/hosts and services after sites were inserted. On failure
// the sites are removed again and the previous state restored.
func (c *Core) goLive(ctx context.Context, sites []store.Site) (bool, error) {
	rollback := func() {
		for _, s := range sites {
			_ = c.Store.RemoveSite(ctx, s.Name)
		}
	}
	if err := c.syncLocal(ctx); err != nil {
		rollback()
		return false, err
	}
	applied, err := c.applyIfRunning(ctx)
	if err != nil {
		rollback()
		_ = c.syncLocal(ctx)
		_, _ = c.applyIfRunning(ctx)
		return false, err
	}
	return applied, nil
}

// UpdateSiteRequest changes an existing site. Empty fields are left as
// they are.
type UpdateSiteRequest struct {
	Name   string
	PHP    string
	Path   string
	Domain string
	// Enabled turns serving the site on or off when set.
	Enabled *bool
	// Aliases, when set, replace the site's extra domains (empty clears).
	Aliases *[]string
	// Force accepts a new folder without index.php or index.html.
	Force bool
}

// maxAliases keeps the certificate and /etc/hosts block reasonable.
const maxAliases = 20

// checkDomainFree fails if domain is served by a site other than self, or
// is already one of self's names.
func (c *Core) checkDomainFree(ctx context.Context, domain, self string) error {
	switch domain {
	case caddy.AdminerDomain:
		return errcode.Errorf(errcode.Conflict, domain+" is reserved for Adminer")
	case caddy.MailDomain:
		return errcode.Errorf(errcode.Conflict, domain+" is reserved for Mailpit")
	}
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return err
	}
	for _, s := range sites {
		if s.Domain == domain || slices.Contains(s.Aliases, domain) {
			if s.Name == self {
				return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s is already a name of %s", domain, self))
			}
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s is used by site %s", domain, s.Name))
		}
	}
	return nil
}

// checkAliases validates and normalizes new aliases for site.
func (c *Core) checkAliases(ctx context.Context, site store.Site, aliases []string) ([]string, error) {
	out := []string{}
	for _, a := range aliases {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" || slices.Contains(out, a) {
			continue
		}
		if a == site.Domain {
			return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%s is the site's own domain", a))
		}
		if err := c.checkSiteDomain(ctx, a); err != nil {
			return nil, err
		}
		if !slices.Contains(site.Aliases, a) {
			if err := c.checkDomainFree(ctx, a, site.Name); err != nil {
				return nil, err
			}
		}
		out = append(out, a)
	}
	if len(out) > maxAliases {
		return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("at most %d aliases per site", maxAliases))
	}
	slices.Sort(out)
	return out, nil
}

// UpdateSite changes a site's PHP version, folder or domain, or turns it on
// or off, installing the PHP version if needed. If Covo is running the change goes live
// immediately; if that fails the previous settings are restored.
func (c *Core) UpdateSite(ctx context.Context, req UpdateSiteRequest) (SiteResult, error) {
	var res SiteResult
	err := c.locked(ctx, func() error {
		old, err := c.Store.Site(ctx, req.Name)
		if err != nil {
			return err
		}
		site := old
		if req.PHP != "" && req.PHP != old.PHPMinor {
			if err := validMinor(req.PHP); err != nil {
				return err
			}
			site.PHPMinor = req.PHP
		}
		if req.Path != "" && req.Path != old.Path {
			if err := c.checkSiteFolder(req.Path, req.Force); err != nil {
				return err
			}
			site.Path = req.Path
		}
		if req.Domain != "" && req.Domain != old.Domain {
			if err := c.checkSiteDomain(ctx, req.Domain); err != nil {
				return err
			}
			if err := c.checkDomainFree(ctx, req.Domain, ""); err != nil {
				return err
			}
			site.Domain = req.Domain
		}
		if req.Enabled != nil {
			site.Enabled = *req.Enabled
		}
		if req.Aliases != nil {
			if site.Aliases, err = c.checkAliases(ctx, site, *req.Aliases); err != nil {
				return err
			}
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			return err
		}
		res = SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}
		if siteEqual(site, old) {
			res.Applied, err = c.applyIfRunning(ctx)
			return err
		}
		if site.PHPMinor != old.PHPMinor {
			if _, err := c.ensurePHP(ctx, site.PHPMinor); err != nil {
				return err
			}
		}
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			return err
		}
		if err := c.Store.SetAliases(ctx, site.Name, site.Aliases); err != nil {
			_ = c.Store.UpdateSite(ctx, old)
			return err
		}
		restore := func() {
			_ = c.Store.UpdateSite(ctx, old)
			_ = c.Store.SetAliases(ctx, old.Name, old.Aliases)
			_ = c.syncLocal(ctx)
			_, _ = c.applyIfRunning(ctx)
		}
		if err := c.syncLocal(ctx); err != nil {
			restore()
			return err
		}
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			restore()
			return err
		}
		return nil
	})
	return res, err
}

func siteEqual(a, b store.Site) bool {
	return a.Path == b.Path && a.PHPMinor == b.PHPMinor && a.Domain == b.Domain &&
		a.HTTPS == b.HTTPS && a.Enabled == b.Enabled && slices.Equal(a.Aliases, b.Aliases) &&
		maps.Equal(a.PHPSettings, b.PHPSettings)
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
		// Its logs are Covo's own files; the site's folder is never touched.
		_ = os.RemoveAll(c.siteLogDir(name))
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
