// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/importers"
	"github.com/benbowdev/benbow/internal/mamp"
	"github.com/benbowdev/benbow/internal/store"
)

// EnvMAMPSettings points the importer at another settings6.plist (tests).
const EnvMAMPSettings = "BENBOW_MAMP_SETTINGS"

// EnvImportHome replaces the home folder (and / for XAMPP) where the other
// importers look (tests).
const EnvImportHome = "BENBOW_IMPORT_HOME"

// mampDB is MAMP PRO's MySQL with its default credentials.
var mampDB = mamp.DBServer{Host: "127.0.0.1", Port: 3306, Socket: "/Applications/MAMP/tmp/mysql/mysql.sock", User: "root", Password: "root"}

// legacyPHP is where PHP versions older than any Benbow offers go.
const legacyPHP = "8.1"

// Import actions.
const (
	ImportCreate = "create"
	ImportSkip   = "skip"
)

// ImportItem is what happens to one MAMP PRO host.
type ImportItem struct {
	MAMP   mamp.Host `json:"mamp"`
	Action string    `json:"action"`
	// Reason says why a host is skipped.
	Reason string `json:"reason,omitempty"`
	// Name, Domain and PHP are the Benbow site, for "create".
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
	PHP    string `json:"php,omitempty"`
	// Aliases are MAMP's aliases mapped like the domain.
	Aliases []string `json:"aliases,omitempty"`
	// Database is the MAMP database linked to the new site because it
	// already exists on Benbow's MySQL (only when MAMP linked exactly one).
	Database string `json:"database,omitempty"`
	// Warnings are differences from MAMP the user should know about.
	Warnings []string `json:"warnings"`
	// Migration is what migrating the new site does (with Migrate).
	Migration *MigrationPlan `json:"migration,omitempty"`
}

// ImportPlan is the outcome of an import, or what it would do.
type ImportPlan struct {
	Source string       `json:"source"`
	DryRun bool         `json:"dry_run"`
	Items  []ImportItem `json:"items"`
	Create int          `json:"create"`
	Skip   int          `json:"skip"`
	// NeedsLocal is true when sites keep .local names and .local domains
	// are not enabled yet.
	NeedsLocal bool `json:"needs_local"`
	// NeedsTest: same for .test names (Valet, Herd).
	NeedsTest bool `json:"needs_test"`
	// Databases the source links to its hosts.
	Databases []string `json:"databases"`
	Applied   bool     `json:"applied"`
	// Kind is the source: mamp, local, herd, valet or xampp.
	Kind string `json:"kind"`
	// Migrated counts the sites whose migration succeeded.
	Migrated int `json:"migrated,omitempty"`
}

// ImportOptions tunes an import.
type ImportOptions struct {
	// Source is mamp (default), local, herd, valet or xampp.
	Source string
	DryRun bool
	// Sites limits the import to these hosts (server name, or the Benbow
	// site name it would get); empty imports every host.
	Sites []string
	// Migrate also copies each new site's database and points its
	// configuration at Benbow (see MigrateOptions).
	Migrate   bool
	Migration MigrateOptions
	// Localhost serves imported sites as <name>.localhost instead of keeping
	// their .local names.
	Localhost bool
}

// ImportMAMP recreates MAMP PRO's hosts as Benbow sites, same names and
// document roots. MAMP is never modified: its settings are read from a
// temporary copy. With DryRun nothing is created.
func (c *Core) ImportMAMP(ctx context.Context, opts ImportOptions) (ImportPlan, error) {
	opts.Source = importers.MAMP
	return c.Import(ctx, opts)
}

// readImportSource reads the hosts of a source tool, only reading its files.
func (c *Core) readImportSource(ctx context.Context, source string) ([]mamp.Host, string, error) {
	home := os.Getenv(EnvImportHome)
	if home == "" {
		var err error
		if home, err = os.UserHomeDir(); err != nil {
			return nil, "", fmt.Errorf("resolve home directory: %w", err)
		}
	}
	switch source {
	case "", importers.MAMP:
		src := os.Getenv(EnvMAMPSettings)
		if src == "" {
			var err error
			if src, err = mamp.DefaultSettings(); err != nil {
				return nil, "", err
			}
		}
		hosts, err := mamp.ReadCopy(ctx, src, c.Layout.Tmp)
		for i := range hosts {
			db := mampDB
			hosts[i].DB = &db
		}
		return hosts, src, err
	case importers.Local:
		h, err := importers.ReadLocal(home)
		return h, "Local", err
	case importers.Herd:
		h, err := importers.ReadHerd(home)
		return h, "Laravel Herd", err
	case importers.Valet:
		h, err := importers.ReadValet(home)
		return h, "Laravel Valet", err
	case importers.XAMPP:
		root := "/"
		if os.Getenv(EnvImportHome) != "" {
			root = home
		}
		h, err := importers.ReadXAMPP(root)
		return h, "XAMPP", err
	}
	return nil, "", errcode.Newf(errcode.InvalidInput, "unknown source %q (%s)", source, strings.Join(importers.Names, ", "))
}

// Import recreates another tool's sites as Benbow sites, same names and
// folders, reading its configuration only. With Migrate, each new site's
// database is copied and its configuration pointed at Benbow. With DryRun
// nothing changes.
func (c *Core) Import(ctx context.Context, opts ImportOptions) (ImportPlan, error) {
	if opts.Source == "" {
		opts.Source = importers.MAMP
	}
	hosts, src, err := c.readImportSource(ctx, opts.Source)
	if err != nil {
		return ImportPlan{}, err
	}
	var plan ImportPlan
	err = c.locked(ctx, func() error {
		var err error
		if plan, err = c.planImport(ctx, src, hosts, opts); err != nil {
			return err
		}
		plan.Kind = opts.Source
		if opts.Migrate {
			if opts.Migration.DumpFile != "" && plan.Create != 1 {
				return errcode.Errorf(errcode.InvalidInput, "a dump file is for one site: choose it with --site")
			}
			for i := range plan.Items {
				it := &plan.Items[i]
				if it.Action != ImportCreate {
					continue
				}
				m := c.planMigration(ctx, previewSite(*it), it.MAMP, httpsURL(it.Domain, c.publicHTTPS(ctx)), opts.Migration)
				it.Migration = &m
			}
		}
		if opts.DryRun || plan.Create == 0 {
			return nil
		}
		if plan.NeedsLocal {
			return errcode.Errorf(errcode.ConfirmationRequired,
				"these sites keep their .local names, which needs `benbow domains enable local` first; or pass --localhost to serve them as <name>.localhost")
		}
		if plan.NeedsTest {
			return errcode.Errorf(errcode.ConfirmationRequired,
				"these sites keep their .test names, which needs `benbow domains enable test` first; or pass --localhost to serve them as <name>.localhost")
		}
		var added []store.Site
		for _, it := range plan.Items {
			if it.Action != ImportCreate {
				continue
			}
			docroot, appRoot, fw := detectFramework(it.MAMP.DocumentRoot)
			site, err := c.insertSite(ctx, AddSiteRequest{Name: it.Name, Domain: it.Domain, PHP: it.PHP, Path: docroot, Force: true, appRoot: appRoot, framework: fw})
			if err != nil {
				for _, s := range added {
					_ = c.Store.RemoveSite(ctx, s.Name)
				}
				return fmt.Errorf("import %s: %w", it.MAMP.ServerName, err)
			}
			added = append(added, site)
			if len(it.Aliases) > 0 {
				if err := c.Store.SetAliases(ctx, site.Name, it.Aliases); err != nil {
					for _, s := range added {
						_ = c.Store.RemoveSite(ctx, s.Name)
					}
					return fmt.Errorf("import %s: %w", it.MAMP.ServerName, err)
				}
			}
		}
		if plan.Applied, err = c.goLive(ctx, added); err != nil {
			return err
		}
		if !opts.Migrate {
			c.linkImportedDatabases(ctx, &plan)
		}
		return nil
	})
	if err != nil || opts.DryRun || !opts.Migrate {
		return plan, err
	}
	// Each site's migration on its own: one failing leaves the others.
	for i := range plan.Items {
		it := &plan.Items[i]
		if it.Action != ImportCreate || it.Migration == nil || it.Migration.Problem != "" {
			continue
		}
		site, err := c.Store.Site(ctx, it.Name)
		if err != nil {
			it.Migration.Error = err.Error()
			continue
		}
		m, err := c.migrateSite(ctx, opts.Source, site, it.MAMP, siteURL(site, c.publicHTTPS(ctx)), opts.Migration)
		if err != nil {
			m.Error = err.Error()
		}
		*it.Migration = m
		if m.Done {
			plan.Migrated++
		}
	}
	return plan, nil
}

// previewSite is the site an import item would create, for planning.
func previewSite(it ImportItem) store.Site {
	docroot, appRoot, fw := detectFramework(it.MAMP.DocumentRoot)
	return store.Site{Name: it.Name, Domain: it.Domain, Path: docroot, AppRoot: appRoot, Framework: fw, PHPMinor: it.PHP}
}

// publicHTTPS is the HTTPS port in addresses.
func (c *Core) publicHTTPS(ctx context.Context) int {
	p, _, err := c.httpsPort(ctx)
	if err != nil {
		return defaultHTTPSPort
	}
	return p
}

// linkImportedDatabases links each new site to its MAMP database when MAMP
// linked exactly one and a database of that name exists on Benbow's MySQL
// (e.g. copied over with mysqldump). Best effort: without a running MySQL
// nothing is linked. Callers hold the lock.
func (c *Core) linkImportedDatabases(ctx context.Context, plan *ImportPlan) {
	dbs, err := c.Databases(ctx)
	if err != nil {
		return
	}
	for i := range plan.Items {
		it := &plan.Items[i]
		if it.Action != ImportCreate || len(it.MAMP.Databases) != 1 || !slices.Contains(dbs, it.MAMP.Databases[0]) {
			continue
		}
		site, err := c.Store.Site(ctx, it.Name)
		if err != nil || site.Database != "" {
			continue
		}
		site.Database = it.MAMP.Databases[0]
		if c.Store.UpdateSite(ctx, site) == nil {
			it.Database = site.Database
		}
	}
}

func (c *Core) planImport(ctx context.Context, src string, hosts []mamp.Host, opts ImportOptions) (ImportPlan, error) {
	plan := ImportPlan{Source: src, DryRun: opts.DryRun, Items: []ImportItem{}, Databases: []string{}}
	existing, err := c.Store.ListSites(ctx)
	if err != nil {
		return plan, err
	}
	names, domains := map[string]bool{}, map[string]bool{}
	for _, s := range existing {
		names[s.Name], domains[s.Domain] = true, true
		for _, a := range s.Aliases {
			domains[a] = true
		}
	}
	localOn, err := c.domainEnabled(ctx, suffixLocal)
	if err != nil {
		return plan, err
	}
	testOn, err := c.domainEnabled(ctx, suffixTest)
	if err != nil {
		return plan, err
	}
	available := c.Manifest.Versions("php", runtime.GOARCH)
	home, err := os.UserHomeDir()
	if err != nil {
		return plan, fmt.Errorf("resolve home directory: %w", err)
	}

	for _, h := range hosts {
		it := ImportItem{MAMP: h, Action: ImportSkip, Warnings: []string{}}
		plan.Databases = append(plan.Databases, h.Databases...)
		skip := func(reason string) {
			it.Reason = reason
			plan.Items = append(plan.Items, it)
			plan.Skip++
		}
		if h.ServerName == "localhost" {
			skip("MAMP's default host")
			continue
		}
		domain, name, warn, err := importDomain(h.ServerName, opts.Localhost)
		if err != nil {
			skip(err.Error())
			continue
		}
		it.Warnings = append(it.Warnings, warn...)
		if len(opts.Sites) > 0 && !slices.Contains(opts.Sites, h.ServerName) && !slices.Contains(opts.Sites, name) {
			skip("not selected")
			continue
		}
		if domains[domain] {
			skip("a Benbow site already uses " + domain)
			continue
		}
		base := name
		for i := 2; names[name]; i++ {
			name = base + "-" + strconv.Itoa(i)
		}
		if _, err := os.Stat(h.DocumentRoot); err != nil {
			skip("folder not found: " + h.DocumentRoot)
			continue
		}
		if err := checkSiteDir(h.DocumentRoot, home, c.Layout.Root); err != nil {
			skip(err.Error())
			continue
		}
		if !hasIndex(h.DocumentRoot) {
			it.Warnings = append(it.Warnings, "no index.php or index.html in the folder")
		}
		version := h.PHPVersion
		if version == "" {
			// The tool's default version: unknown here.
			version = c.recommendedPHP()
			it.Warnings = append(it.Warnings, "PHP version not set in "+importers.Title(opts.Source)+": uses PHP "+version)
		}
		php, warn, err := importPHP(version, available)
		if err != nil {
			skip(err.Error())
			continue
		}
		it.Warnings = append(it.Warnings, warn...)
		for _, a := range h.Aliases {
			ad, _, _, err := importDomain(a, opts.Localhost)
			switch {
			case err != nil:
				it.Warnings = append(it.Warnings, "alias "+a+" not imported: "+err.Error())
			case ad == domain || slices.Contains(it.Aliases, ad):
			case domains[ad]:
				it.Warnings = append(it.Warnings, "alias "+a+" not imported: a Benbow site already uses "+ad)
			case !strings.HasSuffix(ad, "."+suffixLocal) && c.checkSiteDomain(ctx, ad) != nil:
				it.Warnings = append(it.Warnings, "alias "+a+" not imported: "+c.checkSiteDomain(ctx, ad).Error())
			default:
				it.Aliases = append(it.Aliases, ad)
				if strings.HasSuffix(ad, "."+suffixLocal) && !localOn {
					plan.NeedsLocal = true
				}
			}
		}
		if len(h.Databases) > 0 {
			it.Warnings = append(it.Warnings, "databases not imported: "+strings.Join(h.Databases, ", "))
		}
		it.Action, it.Name, it.Domain, it.PHP = ImportCreate, name, domain, php
		names[name], domains[domain] = true, true
		for _, a := range it.Aliases {
			domains[a] = true
		}
		if strings.HasSuffix(domain, "."+suffixLocal) && !localOn {
			plan.NeedsLocal = true
		}
		if strings.HasSuffix(domain, "."+suffixTest) && !testOn {
			plan.NeedsTest = true
		}
		plan.Items = append(plan.Items, it)
		plan.Create++
	}
	slices.Sort(plan.Databases)
	plan.Databases = slices.Compact(plan.Databases)
	return plan, nil
}

// importDomain maps a MAMP server name to a Benbow domain and site name.
func importDomain(serverName string, toLocalhost bool) (domain, name string, warnings []string, err error) {
	n := strings.ToLower(strings.TrimSuffix(serverName, "."))
	if n != serverName {
		warnings = append(warnings, fmt.Sprintf("name %q lowercased", serverName))
	}
	label, isLocal := strings.CutSuffix(n, "."+suffixLocal)
	switch {
	case isLocal:
	case !strings.Contains(n, "."):
		warnings = append(warnings, fmt.Sprintf("MAMP served it as %q; Benbow needs a reserved suffix", n))
		label = n
	default:
		if s, err := domainSuffix(n); err == nil && s != suffixLocal {
			label := strings.TrimSuffix(n, "."+s)
			if toLocalhost && s == suffixTest {
				return label + "." + suffixLocalhost, siteName(label), warnings, nil
			}
			return n, siteName(label), warnings, nil
		}
		return "", "", nil, fmt.Errorf("%q is not under .local, .test or .localhost", serverName)
	}
	domain = label + "." + suffixLocal
	if toLocalhost {
		domain = label + "." + suffixLocalhost
	}
	if !domainRe.MatchString(domain) {
		return "", "", nil, fmt.Errorf("%q is not a valid host name", serverName)
	}
	return domain, siteName(label), warnings, nil
}

// siteName turns "shop.client" into "shop-client".
func siteName(label string) string {
	n := strings.ReplaceAll(label, ".", "-")
	if len(n) > 63 {
		n = strings.TrimRight(n[:63], "-")
	}
	return n
}

// importPHP maps a MAMP PHP version to one Benbow offers.
func importPHP(version string, available []string) (string, []string, error) {
	minor := minorOf(version)
	if validMinor(minor) != nil {
		return "", nil, fmt.Errorf("unknown PHP version %q", version)
	}
	var minors []string
	for _, v := range available {
		if m := minorOf(v); !slices.Contains(minors, m) {
			minors = append(minors, m)
		}
	}
	if slices.Contains(minors, minor) {
		return minor, nil, nil
	}
	if major, _ := strconv.Atoi(strings.SplitN(minor, ".", 2)[0]); major < 8 && slices.Contains(minors, legacyPHP) {
		return legacyPHP, []string{fmt.Sprintf("PHP %s is not available: uses PHP %s; check the site still works", version, legacyPHP)}, nil
	}
	for _, m := range minors { // sorted ascending: the closest newer one
		if store.CompareVersions(m, minor) > 0 {
			return m, []string{fmt.Sprintf("PHP %s is not available: uses PHP %s", version, m)}, nil
		}
	}
	return "", nil, errors.New("no PHP version available for " + version)
}
