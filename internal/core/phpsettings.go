// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/php"
	"github.com/getcovo/covo/internal/store"
)

func settingPHP(minor string) string { return "php_settings_" + minor }

// PHPSettings are the php.ini values of one PHP minor version.
type PHPSettings struct {
	PHP string `json:"php"`
	php.Settings
}

// PHPSettingsResult reports what SetPHPSettings did.
type PHPSettingsResult struct {
	PHPSettings
	Changed bool `json:"changed"`
	// Applied is false when Covo is stopped: PHP-FPM picks it up on start.
	Applied bool `json:"applied"`
}

func (c *Core) phpSettings(ctx context.Context, minor string) (php.Settings, error) {
	s := php.DefaultSettings()
	v, ok, err := c.Store.Setting(ctx, settingPHP(minor))
	if err != nil || !ok {
		return s, err
	}
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return php.DefaultSettings(), fmt.Errorf("read PHP %s settings: %w", minor, err)
	}
	return s, s.Validate()
}

// GetPHPSettings returns the php.ini values of a PHP minor version.
func (c *Core) GetPHPSettings(ctx context.Context, minor string) (PHPSettings, error) {
	if err := validMinor(minor); err != nil {
		return PHPSettings{}, err
	}
	s, err := c.phpSettings(ctx, minor)
	return PHPSettings{PHP: minor, Settings: s}, err
}

// SetPHPSettings changes php.ini values (name → value, e.g. "memory_limit"
// → "1G") for a PHP minor version and restarts its PHP-FPM if Covo is
// running. Nothing is saved unless every value is valid.
func (c *Core) SetPHPSettings(ctx context.Context, minor string, values map[string]string) (PHPSettingsResult, error) {
	res := PHPSettingsResult{PHPSettings: PHPSettings{PHP: minor}}
	if err := validMinor(minor); err != nil {
		return res, err
	}
	err := c.locked(ctx, func() error {
		old, err := c.phpSettings(ctx, minor)
		if err != nil {
			return err
		}
		s := old
		for name, v := range values {
			if err := s.Set(name, v); err != nil {
				return err
			}
		}
		res.Settings = s
		if s == old {
			return nil
		}
		body, err := json.Marshal(s)
		if err != nil {
			return fmt.Errorf("encode PHP settings: %w", err)
		}
		if err := c.Store.SetSetting(ctx, settingPHP(minor), string(body)); err != nil {
			return err
		}
		res.Changed = true
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			_ = c.Store.SetSetting(ctx, settingPHP(minor), mustJSON(old))
			_, _ = c.applyIfRunning(ctx)
			return err
		}
		return nil
	})
	return res, err
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// sitePHPSettings are a site's effective php.ini values: its PHP version's
// with the site's own on top.
func (c *Core) sitePHPSettings(ctx context.Context, site store.Site) (php.Settings, error) {
	base, err := c.phpSettings(ctx, site.PHPMinor)
	if err != nil {
		return base, err
	}
	return base.WithOverrides(site.PHPSettings)
}

// SitePHPSettings are a site's php.ini values and which of them are its own.
type SitePHPSettings struct {
	Site string `json:"site"`
	PHP  string `json:"php"`
	// Effective values for the site's requests.
	php.Settings
	// Overrides are the site's own values; the rest come from its PHP version.
	Overrides map[string]string `json:"overrides"`
	Applied   bool              `json:"applied,omitempty"`
}

// GetSitePHPSettings returns a site's php.ini values.
func (c *Core) GetSitePHPSettings(ctx context.Context, name string) (SitePHPSettings, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return SitePHPSettings{}, err
	}
	s, err := c.sitePHPSettings(ctx, site)
	return SitePHPSettings{Site: name, PHP: site.PHPMinor, Settings: s, Overrides: site.PHPSettings}, err
}

// SetSitePHPSettings sets php.ini values for one site (name → value); an
// empty value removes the site's own value so its PHP version's applies.
// Applied at once if Covo is running.
func (c *Core) SetSitePHPSettings(ctx context.Context, name string, values map[string]string) (SitePHPSettings, error) {
	var res SitePHPSettings
	err := c.locked(ctx, func() error {
		old, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		site := old
		if site.PHPSettings, err = withSitePHPValues(old.PHPSettings, values); err != nil {
			return err
		}
		effective, err := c.sitePHPSettings(ctx, site)
		if err != nil {
			return err
		}
		res = SitePHPSettings{Site: name, PHP: site.PHPMinor, Settings: effective, Overrides: site.PHPSettings}
		if maps.Equal(site.PHPSettings, old.PHPSettings) {
			return nil
		}
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			return err
		}
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			_ = c.Store.UpdateSite(ctx, old)
			_, _ = c.applyIfRunning(ctx)
			return err
		}
		return nil
	})
	return res, err
}

// withSitePHPValues returns current with values applied (name → value; ""
// removes the site's own value), each checked and normalized.
func withSitePHPValues(current, values map[string]string) (map[string]string, error) {
	out := maps.Clone(current)
	if out == nil {
		out = map[string]string{}
	}
	for k, v := range values {
		if !slices.Contains(php.SettingNames, k) {
			return nil, errcode.Newf(errcode.InvalidInput,
				"unknown PHP setting %q; Covo manages %s", k, strings.Join(php.SettingNames, ", "))

		}
		if strings.TrimSpace(v) == "" {
			delete(out, k)
			continue
		}
		probe := php.DefaultSettings()
		if err := probe.Set(k, v); err != nil {
			return nil, err
		}
		out[k] = normalized(probe, k)
	}
	return out, nil
}

// normalized is the stored form of one setting (e.g. "1g" → "1G", "no" → "off").
func normalized(s php.Settings, name string) string {
	switch name {
	case "memory_limit":
		return s.MemoryLimit
	case "upload_max_filesize":
		return s.UploadMaxFilesize
	case "max_execution_time":
		return strconv.Itoa(s.MaxExecutionTime)
	default:
		return map[bool]string{true: "on", false: "off"}[s.DisplayErrors]
	}
}
