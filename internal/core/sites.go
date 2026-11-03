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
	"github.com/getcovo/covo/internal/paths"
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
	// CreateFolder creates Path (missing or empty) for a new, empty site.
	CreateFolder bool
	// Aliases are extra domains for the site.
	Aliases []string
	// Group lists the site in a group (0: top level).
	Group int64
	// PHPSettings are the site's own php.ini values (see SetSitePHPSettings).
	PHPSettings map[string]string
	// NewDatabase creates a database with its own user for the site;
	// LinkDatabase links an existing one instead. Both need MySQL running.
	NewDatabase  bool
	LinkDatabase string
}

// SiteResult is a site with its URL and whether services were (re)applied.
type SiteResult struct {
	SiteStatus
	// Applied is true when Covo was running and the change is already live.
	Applied bool `json:"applied"`
}

// AddSite registers a site, installing its PHP version if needed, with its
// aliases, group, php.ini values and database. If Covo is running the site
// goes live immediately. If any step fails, everything it did is undone:
// the site, its database and the folder it created.
func (c *Core) AddSite(ctx context.Context, req AddSiteRequest) (SiteResult, error) {
	if req.Domain == "" {
		req.Domain = req.Name + "." + suffixLocalhost
	}
	if req.NewDatabase && req.LinkDatabase != "" {
		return SiteResult{}, errcode.Errorf(errcode.InvalidInput, "choose a new database or an existing one, not both")
	}
	if req.NewDatabase || req.LinkDatabase != "" {
		if _, _, _, err := c.mysqlRoot(ctx); err != nil {
			return SiteResult{}, err
		}
	}
	created := false
	if req.CreateFolder {
		if err := c.checkSiteFields(req); err != nil {
			return SiteResult{}, err
		}
		if err := paths.CheckSafe(req.Path); err != nil {
			return SiteResult{}, err
		}
		var err error
		if created, err = prepareEmptyDir(req.Path); err != nil {
			return SiteResult{}, err
		}
		req.Force = true
	}
	undoFolder := func() {
		if req.CreateFolder {
			cleanupDir(req.Path, created)
		}
	}
	if err := c.checkSiteRequest(req); err != nil {
		undoFolder()
		return SiteResult{}, err
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
	if err != nil {
		undoFolder()
		return res, err
	}
	if req.NewDatabase || req.LinkDatabase != "" {
		var dbErr error
		if req.NewDatabase {
			_, dbErr = c.CreateSiteDatabase(ctx, req.Name, "")
		} else {
			_, dbErr = c.LinkSiteDatabase(ctx, req.Name, req.LinkDatabase)
		}
		if dbErr != nil {
			_, _ = c.RemoveSite(context.WithoutCancel(ctx), req.Name)
			undoFolder()
			return SiteResult{}, dbErr
		}
		if res.Site, err = c.Store.Site(ctx, req.Name); err != nil {
			return res, err
		}
	}
	return res, nil
}

// checkSiteRequest validates a request without touching any state.
func (c *Core) checkSiteRequest(req AddSiteRequest) error {
	if err := c.checkSiteFields(req); err != nil {
		return err
	}
	return c.checkSiteFolder(req.Path, req.Force)
}

// checkSiteFields validates everything in a request but its folder.
func (c *Core) checkSiteFields(req AddSiteRequest) error {
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
	_, err := withSitePHPValues(nil, req.PHPSettings)
	return err
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
	groups, err := c.Store.ListGroups(ctx)
	if err != nil {
		return store.Site{}, err
	}
	if !groupExists(groups, req.Group) {
		return store.Site{}, errcode.Errorf(errcode.NotFound, fmt.Sprintf("group %d not found", req.Group))
	}
	settings, err := withSitePHPValues(nil, req.PHPSettings)
	if err != nil {
		return store.Site{}, err
	}
	aliases, err := c.checkAliases(ctx, store.Site{Name: req.Name, Domain: req.Domain}, req.Aliases)
	if err != nil {
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
		Aliases:     aliases,
		PHPSettings: settings,
		GroupID:     req.Group,
		CreatedAt:   time.Now(),
	}
	if err := c.Store.AddSite(ctx, site); err != nil {
		return store.Site{}, err
	}
	if err := c.Store.SetAliases(ctx, site.Name, aliases); err != nil {
		_ = c.Store.RemoveSite(ctx, site.Name)
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
	// Archived puts the site away (also turning it off) or brings it back
	// (turning it on) when set.
	Archived *bool
	// Aliases, when set, replace the site's extra domains (empty clears).
	Aliases *[]string
	// Group, when set, moves the site into a group (0: top level).
	Group *int64
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
		if req.Archived != nil {
			site.Archived, site.Enabled = *req.Archived, !*req.Archived
		}
		if req.Enabled != nil {
			if *req.Enabled && site.Archived {
				return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s is archived: unarchive it first", site.Name))
			}
			site.Enabled = *req.Enabled
		}
		if req.Aliases != nil {
			if site.Aliases, err = c.checkAliases(ctx, site, *req.Aliases); err != nil {
				return err
			}
		}
		if req.Group != nil {
			groups, err := c.Store.ListGroups(ctx)
			if err != nil {
				return err
			}
			if !groupExists(groups, *req.Group) {
				return errcode.Errorf(errcode.NotFound, fmt.Sprintf("group %d not found", *req.Group))
			}
			site.GroupID = *req.Group
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			return err
		}
		res = SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}
		if site.GroupID != old.GroupID {
			// The group only matters to the list: store it, nothing to apply.
			moved := old
			moved.GroupID = site.GroupID
			if err := c.Store.UpdateSite(ctx, moved); err != nil {
				return err
			}
			if siteEqual(site, moved) {
				_, res.Applied, err = c.Store.Setting(ctx, settingRunning)
				return err
			}
			old = moved
		}
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
		a.HTTPS == b.HTTPS && a.Enabled == b.Enabled && a.Archived == b.Archived && slices.Equal(a.Aliases, b.Aliases) &&
		maps.Equal(a.PHPSettings, b.PHPSettings)
}

// RemoveSiteAndDatabase deletes the site's database (and its own MySQL
// user) first, then removes the site. If the database cannot be deleted the
// site is kept. The folder is never touched.
func (c *Core) RemoveSiteAndDatabase(ctx context.Context, name string) (SiteResult, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SiteResult{}, err
	}
	if site.Database != "" {
		if _, err := c.UnlinkSiteDatabase(ctx, name, true); err != nil {
			return SiteResult{}, err
		}
	}
	return c.RemoveSite(ctx, name)
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
