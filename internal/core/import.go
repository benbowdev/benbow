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

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mamp"
	"github.com/getcovo/covo/internal/store"
)

// EnvMAMPSettings points the importer at another settings6.plist (tests).
const EnvMAMPSettings = "COVO_MAMP_SETTINGS"

// legacyPHP is where PHP versions older than any Covo offers go.
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
	// Name, Domain and PHP are the Covo site, for "create".
	Name   string `json:"name,omitempty"`
	Domain string `json:"domain,omitempty"`
	PHP    string `json:"php,omitempty"`
	// Warnings are differences from MAMP the user should know about.
	Warnings []string `json:"warnings"`
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
	// Databases are listed, never imported: export them from MAMP.
	Databases []string `json:"databases"`
	Applied   bool     `json:"applied"`
}

// ImportOptions tunes an import.
type ImportOptions struct {
	DryRun bool
	// Localhost serves imported sites as <name>.localhost instead of keeping
	// their .local names.
	Localhost bool
}

// ImportMAMP recreates MAMP PRO's hosts as Covo sites, same names and
// document roots. MAMP is never modified: its settings are read from a
// temporary copy. With DryRun nothing is created.
func (c *Core) ImportMAMP(ctx context.Context, opts ImportOptions) (ImportPlan, error) {
	src := os.Getenv(EnvMAMPSettings)
	if src == "" {
		var err error
		if src, err = mamp.DefaultSettings(); err != nil {
			return ImportPlan{}, err
		}
	}
	hosts, err := mamp.ReadCopy(ctx, src, c.Layout.Tmp)
	if err != nil {
		return ImportPlan{}, err
	}
	var plan ImportPlan
	err = c.locked(ctx, func() error {
		var err error
		if plan, err = c.planImport(ctx, src, hosts, opts); err != nil {
			return err
		}
		if opts.DryRun || plan.Create == 0 {
			return nil
		}
		if plan.NeedsLocal {
			return errcode.Errorf(errcode.ConfirmationRequired,
				"these MAMP sites keep their .local names, which needs `covo domains enable local` first; or pass --localhost to serve them as <name>.localhost")
		}
		var added []store.Site
		for _, it := range plan.Items {
			if it.Action != ImportCreate {
				continue
			}
			site, err := c.insertSite(ctx, AddSiteRequest{Name: it.Name, Domain: it.Domain, PHP: it.PHP, Path: it.MAMP.DocumentRoot, Force: true})
			if err != nil {
				for _, s := range added {
					_ = c.Store.RemoveSite(ctx, s.Name)
				}
				return fmt.Errorf("import %s: %w", it.MAMP.ServerName, err)
			}
			added = append(added, site)
		}
		plan.Applied, err = c.goLive(ctx, added)
		return err
	})
	return plan, err
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
	}
	localOn, err := c.domainEnabled(ctx, suffixLocal)
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
		if domains[domain] {
			skip("a Covo site already uses " + domain)
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
		php, warn, err := importPHP(h.PHPVersion, available)
		if err != nil {
			skip(err.Error())
			continue
		}
		it.Warnings = append(it.Warnings, warn...)
		if len(h.Aliases) > 0 {
			it.Warnings = append(it.Warnings, "aliases not imported: "+strings.Join(h.Aliases, ", "))
		}
		if len(h.Databases) > 0 {
			it.Warnings = append(it.Warnings, "databases not imported: "+strings.Join(h.Databases, ", "))
		}
		it.Action, it.Name, it.Domain, it.PHP = ImportCreate, name, domain, php
		names[name], domains[domain] = true, true
		if strings.HasSuffix(domain, "."+suffixLocal) && !localOn {
			plan.NeedsLocal = true
		}
		plan.Items = append(plan.Items, it)
		plan.Create++
	}
	slices.Sort(plan.Databases)
	plan.Databases = slices.Compact(plan.Databases)
	return plan, nil
}

// importDomain maps a MAMP server name to a Covo domain and site name.
func importDomain(serverName string, toLocalhost bool) (domain, name string, warnings []string, err error) {
	n := strings.ToLower(strings.TrimSuffix(serverName, "."))
	if n != serverName {
		warnings = append(warnings, fmt.Sprintf("name %q lowercased", serverName))
	}
	label, isLocal := strings.CutSuffix(n, "."+suffixLocal)
	switch {
	case isLocal:
	case !strings.Contains(n, "."):
		warnings = append(warnings, fmt.Sprintf("MAMP served it as %q; Covo needs a reserved suffix", n))
		label = n
	default:
		if s, err := domainSuffix(n); err == nil && s != suffixLocal {
			return n, siteName(strings.TrimSuffix(n, "."+s)), warnings, nil
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

// importPHP maps a MAMP PHP version to one Covo offers.
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
