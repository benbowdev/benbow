// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/components"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/store"
	"github.com/getcovo/covo/internal/version"
)

const (
	// ReleaseManifestURL is the signed manifest attached to getcovo/covo-binaries'
	// "manifest" release (the signature is at the same URL plus ".sig").
	// Only the daemon's automatic check (at most once every 24 hours) uses
	// it: the asset's daily download count is how many Macs ran Covo, and
	// nothing else is counted (docs/updates.md).
	ReleaseManifestURL = "https://github.com/getcovo/covo-binaries/releases/download/manifest/manifest.json"
	// DefaultManifestURL is the same manifest on covo-binaries' main branch:
	// manual checks use it, and the automatic check falls back to it.
	DefaultManifestURL = "https://raw.githubusercontent.com/getcovo/covo-binaries/main/manifest/manifest.json"
	// EnvManifestURL replaces DefaultManifestURL (tests).
	EnvManifestURL = "COVO_MANIFEST_URL"
	// EnvManifestReleaseURL replaces ReleaseManifestURL (tests).
	EnvManifestReleaseURL = "COVO_MANIFEST_RELEASE_URL"
	// EnvManifestTestKey adds a trusted public key (base64). Only honored
	// together with COVO_HOME, i.e. in an isolated test installation.
	EnvManifestTestKey = "COVO_MANIFEST_TEST_KEY"

	settingUpdatesChecked = "updates_checked_at"
	settingUpdatesError   = "updates_error"
	// settingUpdatesAuto is "off" when the user turned the daily check off.
	settingUpdatesAuto = "updates_auto"
	// settingUpdatesAutoChecked: the last automatic check that succeeded.
	settingUpdatesAutoChecked = "updates_auto_checked_at"
	// autoCheckEvery is the least time between two automatic checks, so a
	// Mac is never counted twice in one day.
	autoCheckEvery  = 24 * time.Hour
	maxManifestSize = 4 << 20
)

// ComponentUpdate is a newer version Covo can switch to.
type ComponentUpdate struct {
	// Component is caddy, mysql, mailpit, adminer, wpcli or php-<minor>.
	Component string `json:"component"`
	Current   string `json:"current"`
	Available string `json:"available"`
	// Blocked says why it cannot be applied yet.
	Blocked string `json:"blocked,omitempty"`
}

// UpdatesStatus is what covo update reports.
type UpdatesStatus struct {
	CheckedAt *time.Time `json:"checked_at,omitempty"`
	// Auto is true when the daemon checks once a day by itself.
	Auto    bool              `json:"auto"`
	Serial  int64             `json:"serial"`
	Error   string            `json:"error,omitempty"`
	Updates []ComponentUpdate `json:"updates"`
	// Applied lists what an update or rollback changed.
	Applied []ComponentUpdate `json:"applied,omitempty"`
	// Live is false when Covo is stopped: changes apply on start.
	Live bool `json:"live,omitempty"`
}

func (c *Core) manifestDir() string { return filepath.Join(c.Layout.Data, "manifest") }

func trustedKeys() ([]ed25519.PublicKey, error) {
	encoded := slices.Clone(components.TrustedKeys)
	if k := os.Getenv(EnvManifestTestKey); allowManifestTestKey && k != "" && os.Getenv("COVO_HOME") != "" {
		encoded = append(encoded, k)
	}
	return components.ParseKeys(encoded)
}

// loadSignedManifest merges the stored signed manifest, if valid and newer.
func (c *Core) loadSignedManifest() {
	body, err := os.ReadFile(filepath.Join(c.manifestDir(), "manifest.json"))
	if err != nil {
		return
	}
	sig, err := os.ReadFile(filepath.Join(c.manifestDir(), "manifest.json.sig"))
	if err != nil {
		return
	}
	keys, err := trustedKeys()
	if err != nil {
		return
	}
	m, err := components.VerifySigned(body, sig, keys)
	if err != nil || m.Serial <= c.Manifest.Serial {
		return
	}
	if merged, err := c.Manifest.Merge(m); err == nil {
		c.Manifest = merged
	}
}

var manifestClient = &http.Client{Transport: version.Transport{}}

func fetch(ctx context.Context, url string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", url, err)
	}
	resp, err := manifestClient.Do(req)
	if err != nil {
		return nil, errcode.New(errcode.Download, fmt.Errorf("fetch %s: %w", url, err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errcode.Newf(errcode.Download, "fetch %s: HTTP %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxManifestSize+1))
	if err != nil {
		return nil, errcode.New(errcode.Download, fmt.Errorf("read %s: %w", url, err))
	}
	if len(body) > maxManifestSize {
		return nil, errcode.Errorf(errcode.Download, url+" is too large")
	}
	return body, nil
}

// CheckUpdates downloads the published manifest from covo-binaries' main
// branch, verifies its signature and expiry, keeps it if it is newer, and
// reports the updates now available. Nothing is installed. This is the
// manual check: it never touches the release asset that counts daily
// checks.
func (c *Core) CheckUpdates(ctx context.Context) (UpdatesStatus, error) {
	return c.runCheck(ctx, false)
}

// AutoCheckUpdates is the daemon's daily check: the release asset first,
// main as a fallback (the asset is briefly missing while the workflow
// replaces it). A success is recorded for AutoCheckDue.
func (c *Core) AutoCheckUpdates(ctx context.Context) (UpdatesStatus, error) {
	return c.runCheck(ctx, true)
}

func (c *Core) runCheck(ctx context.Context, auto bool) (UpdatesStatus, error) {
	err := c.locked(ctx, func() error {
		urls := []string{manifestURL()}
		if auto {
			urls = []string{releaseManifestURL(), manifestURL()}
		}
		err := c.checkUpdates(ctx, urls)
		msg := ""
		if err != nil {
			msg = err.Error()
		}
		now := time.Now().UTC().Format(time.RFC3339)
		_ = c.Store.SetSetting(ctx, settingUpdatesChecked, now)
		_ = c.Store.SetSetting(ctx, settingUpdatesError, msg)
		if auto && err == nil {
			_ = c.Store.SetSetting(ctx, settingUpdatesAutoChecked, now)
		}
		return err
	})
	if err != nil {
		return UpdatesStatus{}, err
	}
	return c.Updates(ctx)
}

func manifestURL() string {
	if u := os.Getenv(EnvManifestURL); u != "" {
		return u
	}
	return DefaultManifestURL
}

func releaseManifestURL() string {
	if u := os.Getenv(EnvManifestReleaseURL); u != "" {
		return u
	}
	return ReleaseManifestURL
}

// AutoCheckDue says whether the daemon should check now: the daily check
// is on, and the last automatic check that succeeded is at least a day old.
func (c *Core) AutoCheckDue(ctx context.Context, now time.Time) (bool, error) {
	on, err := c.autoUpdates(ctx)
	if err != nil || !on {
		return false, err
	}
	var last time.Time
	if v, ok, err := c.Store.Setting(ctx, settingUpdatesAutoChecked); err != nil {
		return false, err
	} else if ok {
		last, _ = time.Parse(time.RFC3339, v)
	}
	return dueForAutoCheck(last, now), nil
}

// dueForAutoCheck: never checked, a day or more since, or a last check in
// the future (the clock went back).
func dueForAutoCheck(last, now time.Time) bool {
	return last.IsZero() || now.Sub(last) >= autoCheckEvery || last.After(now.Add(time.Hour))
}

func (c *Core) autoUpdates(ctx context.Context) (bool, error) {
	v, _, err := c.Store.Setting(ctx, settingUpdatesAuto)
	return v != "off", err
}

// SetAutoUpdates turns the daemon's daily check on or off. Off, Covo makes
// no update request by itself; `covo update check` still works.
func (c *Core) SetAutoUpdates(ctx context.Context, on bool) (UpdatesStatus, error) {
	var err error
	if on {
		err = c.Store.DeleteSetting(ctx, settingUpdatesAuto)
	} else {
		err = c.Store.SetSetting(ctx, settingUpdatesAuto, "off")
	}
	if err != nil {
		return UpdatesStatus{}, err
	}
	return c.Updates(ctx)
}

// checkUpdates takes the first of urls whose manifest downloads, is signed
// by a trusted key and has not expired; the next one is tried otherwise.
func (c *Core) checkUpdates(ctx context.Context, urls []string) error {
	keys, err := trustedKeys()
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return errcode.Errorf(errcode.Unsupported, "this build of Covo has no manifest key: updates come with a newer Covo")
	}
	m, body, sig, err := firstVerified(ctx, urls, keys)
	if err != nil {
		return err
	}
	if m.Serial <= c.Manifest.Serial {
		return nil // nothing newer (or an older copy: never go back)
	}
	merged, err := c.Manifest.Merge(m)
	if err != nil {
		return err
	}
	if _, err := fsutil.WriteAtomic(filepath.Join(c.manifestDir(), "manifest.json"), body, 0o644); err != nil {
		return err
	}
	if _, err := fsutil.WriteAtomic(filepath.Join(c.manifestDir(), "manifest.json.sig"), sig, 0o644); err != nil {
		return err
	}
	c.Manifest = merged
	return nil
}

// firstVerified returns the manifest of the first url that works; the
// error is the last url's.
func firstVerified(ctx context.Context, urls []string, keys []ed25519.PublicKey) (m components.Manifest, body, sig []byte, err error) {
	for _, url := range urls {
		if m, body, sig, err = fetchVerified(ctx, url, keys); err == nil {
			return m, body, sig, nil
		}
	}
	return m, nil, nil, err
}

// fetchVerified downloads url and url+".sig" and returns the manifest if
// the signature and the expiry are good.
func fetchVerified(ctx context.Context, url string, keys []ed25519.PublicKey) (components.Manifest, []byte, []byte, error) {
	body, err := fetch(ctx, url)
	if err != nil {
		return components.Manifest{}, nil, nil, err
	}
	sig, err := fetch(ctx, url+".sig")
	if err != nil {
		return components.Manifest{}, nil, nil, err
	}
	m, err := components.VerifySigned(body, sig, keys)
	if err != nil {
		return components.Manifest{}, nil, nil, err
	}
	if err := m.CheckFresh(time.Now()); err != nil {
		return components.Manifest{}, nil, nil, err
	}
	return m, body, sig, nil
}

// major is the series an update stays in: "8.4" for MySQL (LTS), the
// major version otherwise.
func updateSeries(component, version string) string {
	if component == "mysql" {
		return mysqlSeries
	}
	major, _, _ := strings.Cut(version, ".")
	return major
}

// Updates reports the updates available in the manifest Covo has (it does
// not go online: see CheckUpdates).
func (c *Core) Updates(ctx context.Context) (UpdatesStatus, error) {
	st := UpdatesStatus{Serial: c.Manifest.Serial, Updates: []ComponentUpdate{}}
	auto, err := c.autoUpdates(ctx)
	if err != nil {
		return st, err
	}
	st.Auto = auto
	if v, ok, err := c.Store.Setting(ctx, settingUpdatesChecked); err == nil && ok {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			st.CheckedAt = &t
		}
	}
	if v, ok, err := c.Store.Setting(ctx, settingUpdatesError); err == nil && ok {
		st.Error = v
	}
	for _, comp := range pinnedComponents {
		cur, ok, err := c.Store.Setting(ctx, settingPin(comp))
		if err != nil {
			return st, err
		}
		if !ok {
			continue // never installed
		}
		art, err := c.Manifest.Resolve(comp, updateSeries(comp, cur))
		if err == nil && store.CompareVersions(art.Version, cur) > 0 {
			st.Updates = append(st.Updates, ComponentUpdate{Component: comp, Current: cur, Available: art.Version})
		}
	}
	php, err := c.phpUpdates(ctx)
	if err != nil {
		return st, err
	}
	st.Updates = append(st.Updates, php...)
	return st, nil
}

func (c *Core) phpUpdates(ctx context.Context) ([]ComponentUpdate, error) {
	installed, err := c.Store.ListPHP(ctx)
	if err != nil {
		return nil, err
	}
	var out []ComponentUpdate
	seen := map[string]bool{}
	for _, v := range installed {
		if seen[v.Minor] {
			continue
		}
		seen[v.Minor] = true
		cur, err := c.Store.LatestPHP(ctx, v.Minor)
		if err != nil {
			return nil, err
		}
		art, err := c.Manifest.Resolve("php", v.Minor)
		if err != nil || store.CompareVersions(art.Version, cur.Version) <= 0 {
			continue
		}
		u := ComponentUpdate{Component: "php-" + v.Minor, Current: cur.Version, Available: art.Version}
		if on, err := c.xdebugEnabled(ctx, v.Minor); err != nil {
			return nil, err
		} else if on {
			if _, err := c.Manifest.ResolveFor("xdebug", art.Version); err != nil {
				u.Blocked = "Xdebug is on for PHP " + v.Minor + " and has no build for " + art.Version + " yet"
			}
		}
		out = append(out, u)
	}
	return out, nil
}

// ApplyUpdates installs the given updates (all available ones if names is
// empty), switches to them and restarts what uses them. Downloads are
// verified before anything changes; on failure the previous versions stay.
func (c *Core) ApplyUpdates(ctx context.Context, names []string) (UpdatesStatus, error) {
	var applied []ComponentUpdate
	var live bool
	err := c.locked(ctx, func() error {
		avail, err := c.Updates(ctx)
		if err != nil {
			return err
		}
		var todo []ComponentUpdate
		for _, u := range avail.Updates {
			if len(names) == 0 || slices.Contains(names, u.Component) {
				todo = append(todo, u)
			}
		}
		for _, n := range names {
			if !slices.ContainsFunc(todo, func(u ComponentUpdate) bool { return u.Component == n }) {
				return errcode.Newf(errcode.NotFound, "no update available for %s", n)
			}
		}
		var undo []func()
		rollback := func() {
			for i := len(undo) - 1; i >= 0; i-- {
				undo[i]()
			}
		}
		for _, u := range todo {
			if u.Blocked != "" {
				if len(names) > 0 {
					rollback()
					return errcode.Errorf(errcode.Conflict, u.Component+": "+u.Blocked)
				}
				continue
			}
			fn, err := c.applyOne(ctx, u)
			if err != nil {
				rollback()
				return err
			}
			undo = append(undo, fn)
			applied = append(applied, u)
		}
		if len(applied) == 0 {
			return nil
		}
		if live, err = c.applyIfRunning(ctx); err != nil {
			rollback()
			_, _ = c.applyIfRunning(ctx)
			applied = nil
			return err
		}
		return nil
	})
	if err != nil {
		return UpdatesStatus{}, err
	}
	st, err := c.Updates(ctx)
	st.Applied, st.Live = applied, live
	return st, err
}

// applyOne downloads and verifies u, then switches to it. It returns how
// to switch back.
func (c *Core) applyOne(ctx context.Context, u ComponentUpdate) (func(), error) {
	if minor, ok := strings.CutPrefix(u.Component, "php-"); ok {
		inst, err := c.installPHP(ctx, u.Available)
		if err != nil {
			return nil, err
		}
		if on, err := c.xdebugEnabled(ctx, minor); err == nil && on {
			if _, _, err := c.ensureXdebug(ctx, inst.PHPVersion); err != nil {
				_ = c.Store.RemovePHP(ctx, u.Available)
				return nil, err
			}
		}
		return func() { _ = c.Store.RemovePHP(ctx, u.Available) }, nil
	}
	art, err := c.Manifest.Resolve(u.Component, u.Available)
	if err != nil {
		return nil, err
	}
	if _, err := c.Installer.Install(ctx, art); err != nil {
		return nil, err
	}
	if err := c.Store.SetSetting(ctx, settingPrevious(u.Component), u.Current); err != nil {
		return nil, err
	}
	if err := c.Store.SetSetting(ctx, settingPin(u.Component), u.Available); err != nil {
		return nil, err
	}
	return func() { _ = c.Store.SetSetting(ctx, settingPin(u.Component), u.Current) }, nil
}

// RollbackUpdate goes back to the version a component had before its last
// update (PHP: forgets the newest patch of that minor version).
func (c *Core) RollbackUpdate(ctx context.Context, name string) (UpdatesStatus, error) {
	var done ComponentUpdate
	var live bool
	err := c.locked(ctx, func() error {
		if minor, ok := strings.CutPrefix(name, "php-"); ok {
			versions, err := c.Store.ListPHP(ctx)
			if err != nil {
				return err
			}
			var same []store.PHPVersion
			for _, v := range versions {
				if v.Minor == minor {
					same = append(same, v)
				}
			}
			if len(same) < 2 {
				return errcode.Errorf(errcode.NotFound, "no earlier PHP "+minor+" to go back to")
			}
			slices.SortFunc(same, func(a, b store.PHPVersion) int { return store.CompareVersions(a.Version, b.Version) })
			newest, prev := same[len(same)-1], same[len(same)-2]
			if err := c.Store.RemovePHP(ctx, newest.Version); err != nil {
				return err
			}
			done = ComponentUpdate{Component: name, Current: newest.Version, Available: prev.Version}
		} else {
			if !slices.Contains(pinnedComponents, name) {
				return errcode.Newf(errcode.InvalidInput, "unknown component %q", name)
			}
			prev, ok, err := c.Store.Setting(ctx, settingPrevious(name))
			if err != nil {
				return err
			}
			cur, _, _ := c.Store.Setting(ctx, settingPin(name))
			if !ok || prev == "" || prev == cur {
				return errcode.Errorf(errcode.NotFound, "no earlier "+name+" to go back to")
			}
			if err := c.Store.SetSetting(ctx, settingPin(name), prev); err != nil {
				return err
			}
			if err := c.Store.SetSetting(ctx, settingPrevious(name), cur); err != nil {
				return err
			}
			done = ComponentUpdate{Component: name, Current: cur, Available: prev}
		}
		var err error
		live, err = c.applyIfRunning(ctx)
		return err
	})
	if err != nil {
		return UpdatesStatus{}, err
	}
	st, err := c.Updates(ctx)
	st.Applied, st.Live = []ComponentUpdate{done}, live
	return st, err
}
