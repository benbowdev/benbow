// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"maps"
	"strings"
	"testing"
)

// A wp-config.php that sets WP_HOME and WP_SITEURL wins over the database:
// the migration points them at Benbow's address too, or says what to check.
func TestWPAddressDefines(t *testing.T) {
	const url = "https://acme.localhost:8443"
	tests := []struct {
		name, config, dbHome string
		want                 map[string]string
		warn                 []string // substrings, one per warning
	}{
		{name: "none", config: `define('DB_NAME', 'x');`, dbHome: "http://acme.local", want: map[string]string{}},
		{
			name:   "both at the root",
			config: "define('WP_HOME', 'http://acme.local');\ndefine('WP_SITEURL', 'http://acme.local');",
			dbHome: "http://acme.local",
			want:   map[string]string{"WP_HOME": url, "WP_SITEURL": url},
		},
		{
			name:   "siteurl in a subfolder, other scheme",
			config: "define('WP_HOME', 'http://acme.local/');\ndefine('WP_SITEURL', 'https://acme.local/wp');",
			want:   map[string]string{"WP_HOME": url, "WP_SITEURL": url + "/wp"},
		},
		{
			name:   "only siteurl, home from the database",
			config: "define('WP_SITEURL', 'http://localhost:8888/acme/wp');",
			dbHome: "http://localhost:8888/acme",
			want:   map[string]string{"WP_SITEURL": url + "/wp"},
		},
		{
			name:   "already right",
			config: "define('WP_HOME', '" + url + "');",
			want:   map[string]string{},
		},
		{
			name:   "set from code",
			config: "define('WP_HOME', 'http://' . $_SERVER['HTTP_HOST']);",
			want:   map[string]string{},
			warn:   []string{"sets WP_HOME from code"},
		},
		{
			name:   "defined twice",
			config: "if ($a) { define('WP_HOME', 'http://a.local'); } else { define('WP_HOME', 'http://b.local'); }",
			want:   map[string]string{},
			warn:   []string{"defines WP_HOME 2 times"},
		},
		{
			name:   "siteurl elsewhere",
			config: "define('WP_HOME', 'http://acme.local');\ndefine('WP_SITEURL', 'http://cdn.example.com/wp');",
			want:   map[string]string{"WP_HOME": url},
			warn:   []string{"WP_SITEURL to http://cdn.example.com/wp"},
		},
		{
			name:   "planning: siteurl without a known home",
			config: "define('WP_SITEURL', 'http://acme.local');",
			want:   map[string]string{},
		},
		{
			name:   "a longer host is not under the old home",
			config: "define('WP_SITEURL', 'http://acme.local.example');",
			dbHome: "http://acme.local",
			want:   map[string]string{},
			warn:   []string{"outside the old address"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warnings := wpAddressDefines(tt.config, tt.dbHome, url)
			if !maps.Equal(got, tt.want) {
				t.Errorf("values = %v, want %v", got, tt.want)
			}
			if len(warnings) != len(tt.warn) {
				t.Fatalf("warnings = %q, want %d", warnings, len(tt.warn))
			}
			for i, w := range tt.warn {
				if !strings.Contains(warnings[i], w) {
					t.Errorf("warning %q does not say %q", warnings[i], w)
				}
			}
		})
	}
}

// The plan says the defines will change and warns before anything runs.
func TestWPAddressPlan(t *testing.T) {
	config := "define('WP_HOME', 'http://acme.local');\ndefine('WP_SITEURL', 'http://' . $_SERVER['HTTP_HOST']);\n"
	step, warnings := wpAddressPlan(config, "https://acme.localhost:8443")
	if step != "set WP_HOME in wp-config.php to the new address" {
		t.Errorf("step = %q", step)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "WP_SITEURL from code") {
		t.Errorf("warnings = %q", warnings)
	}
	if step, warnings := wpAddressPlan("define('DB_NAME', 'x');", "https://x"); step != "" || len(warnings) != 0 {
		t.Errorf("no defines: %q %q", step, warnings)
	}
}
