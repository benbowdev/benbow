// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/getcovo/covo/internal/php"
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
