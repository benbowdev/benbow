// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// RenameSiteRequest renames a site. The folder, database and settings stay
// as they are; Domain also moves the address when it starts with the old
// name (shop.localhost → store.localhost).
type RenameSiteRequest struct {
	Name    string `json:"name"`
	NewName string `json:"new_name"`
	Domain  bool   `json:"domain"`
}

// RenameSite renames a site: its record and everything keyed by its name
// (aliases, snapshots, remotes and their keychain passwords, agent and MCP
// records, migration backups, logs), then optionally its address.
func (c *Core) RenameSite(ctx context.Context, req RenameSiteRequest) (SiteResult, error) {
	from, to := req.Name, strings.TrimSpace(strings.ToLower(req.NewName))
	var domain string
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, from)
		if err != nil {
			return err
		}
		if !siteNameRe.MatchString(to) {
			return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("site name %q must be lowercase letters, digits and dashes", to))
		}
		if to == from {
			return errcode.Errorf(errcode.InvalidInput, "the new name is the same as the old one")
		}
		if _, err := c.Store.Site(ctx, to); err == nil {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", to))
		} else if errcode.Code(err) != errcode.NotFound {
			return err
		}
		if _, err := c.Store.OpenAgentSession(ctx, from); err == nil {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("an AI agent session is open on %s: keep or undo it first", from))
		}
		if req.Domain {
			if rest, ok := strings.CutPrefix(site.Domain, from+"."); ok {
				domain = to + "." + rest
			}
		}
		remotes, err := c.Store.ListRemotes(ctx, from)
		if err != nil {
			return err
		}

		// Covo's own folders named after the site move first, back on failure.
		dirs := map[string]string{}
		for _, d := range []func(string) string{c.siteLogDir, c.snapshotDir, c.backupDir} {
			oldDir, newDir := d(from), d(to)
			if _, err := os.Stat(oldDir); err != nil {
				continue
			}
			if _, err := os.Stat(newDir); err == nil {
				return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s already exists: move it away first", newDir))
			}
			dirs[oldDir] = newDir
		}
		moved := map[string]string{}
		undo := func() {
			for o, n := range moved {
				_ = os.Rename(n, o)
			}
		}
		for o, n := range dirs {
			if err := os.MkdirAll(filepath.Dir(n), 0o700); err != nil {
				undo()
				return fmt.Errorf("create %s: %w", filepath.Dir(n), err)
			}
			if err := os.Rename(o, n); err != nil {
				undo()
				return fmt.Errorf("move %s: %w", o, err)
			}
			moved[o] = n
		}
		if err := c.Store.RenameSite(ctx, from, to, dirs); err != nil {
			undo()
			return err
		}

		// Remote passwords live in the keychain under "<site>/<remote>".
		for _, r := range remotes {
			kc, err := mysqlKeychain(c.launchctl.Prefix)
			if err != nil {
				break
			}
			kc.Service, kc.Account = c.remoteKeychainService(), remoteAccount(from, r.Name)
			pw, err := kc.Get(ctx)
			if err != nil || pw == "" {
				continue
			}
			nk := kc
			nk.Account = remoteAccount(to, r.Name)
			if err := nk.Set(ctx, pw); err == nil {
				_, _ = kc.Delete(ctx)
			}
		}

		if share, err := c.phoneShare(ctx); err == nil && share != nil && share.Site == from {
			share.Site, share.Host = to, phoneHost(to, macName())
			b, _ := json.Marshal(share)
			if err := c.Store.SetSetting(ctx, settingPhone, string(b)); err != nil {
				return err
			}
		}
		if _, running, err := c.Store.Setting(ctx, settingRunning); err == nil && running {
			if _, err := c.reconcile(ctx); err != nil {
				return fmt.Errorf("renamed, but serving the new name failed: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return SiteResult{}, err
	}
	if domain != "" {
		res, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: to, Domain: domain})
		if err != nil {
			var e *errcode.Error
			if errors.As(err, &e) {
				return res, errcode.Errorf(e.Code, "renamed to "+to+", but the address did not change: "+e.Err.Error())
			}
			return res, fmt.Errorf("renamed to %s, but the address did not change: %w", to, err)
		}
		return res, nil
	}
	return c.siteResultByName(ctx, to)
}

// siteResultByName is a site with its address.
func (c *Core) siteResultByName(ctx context.Context, name string) (SiteResult, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SiteResult{}, err
	}
	port, _, err := c.httpsPort(ctx)
	if err != nil {
		return SiteResult{}, err
	}
	return SiteResult{SiteStatus: SiteStatus{Site: site, URL: siteURL(site, port)}}, nil
}
