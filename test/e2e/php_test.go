// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"slices"
	"testing"
)

// wordpressModules are the extensions WordPress requires or strongly
// recommends (https://make.wordpress.org/hosting/handbook/server-environment/).
var wordpressModules = []string{
	"curl", "dom", "exif", "fileinfo", "hash", "imagick", "intl", "json",
	"mbstring", "mysqli", "openssl", "pcre", "sodium", "xml", "zip", "Zend OPcache",
}

func TestPHPInstallModules(t *testing.T) {
	h := newHarness(t)
	for _, minor := range []string{"8.4", "8.2"} {
		t.Run(minor, func(t *testing.T) {
			var res struct {
				Version string   `json:"version"`
				Modules []string `json:"modules"`
			}
			if err := json.Unmarshal(h.mustCovo("php", "install", minor), &res); err != nil {
				t.Fatal(err)
			}
			for _, m := range wordpressModules {
				if !slices.Contains(res.Modules, m) {
					t.Errorf("PHP %s lacks %s", res.Version, m)
				}
			}
			// Xdebug is not part of static-php-cli builds; see docs/spike.md.
			if slices.Contains(res.Modules, "xdebug") {
				t.Logf("PHP %s unexpectedly ships Xdebug", res.Version)
			}
		})
	}
}
