// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"testing"
)

func TestImportDomain(t *testing.T) {
	tests := []struct {
		server    string
		localhost bool
		domain    string
		name      string
		warnings  int
		wantErr   bool
	}{
		{"shop.local", false, "shop.local", "shop", 0, false},
		{"shop.local", true, "shop.localhost", "shop", 0, false},
		{"blog.client.local", false, "blog.client.local", "blog-client", 0, false},
		{"Shop.Local", false, "shop.local", "shop", 1, false},
		{"intranet", false, "intranet.local", "intranet", 1, false},
		{"intranet", true, "intranet.localhost", "intranet", 1, false},
		{"demo.benbow.test", false, "demo.benbow.test", "demo-benbow", 0, false},
		{"example.com", false, "", "", 0, true},
		{"bad_name.local", false, "", "", 0, true},
		{"x..local", false, "", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.server, func(t *testing.T) {
			domain, name, warnings, err := importDomain(tt.server, tt.localhost)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v", err)
			}
			if domain != tt.domain || name != tt.name || len(warnings) != tt.warnings {
				t.Fatalf("got %q %q %v", domain, name, warnings)
			}
		})
	}
}

func TestImportPHP(t *testing.T) {
	available := []string{"8.1.34", "8.2.32", "8.4.23"}
	tests := []struct {
		mamp, want string
		warn       bool
		wantErr    bool
	}{
		{"8.1.13", "8.1", false, false},
		{"8.2.0", "8.2", false, false},
		{"8.4.1", "8.4", false, false},
		{"7.4.33", "8.1", true, false},
		{"5.6.40", "8.1", true, false},
		{"8.3.9", "8.4", true, false},
		{"8.0.30", "8.1", true, false},
		{"9.1.0", "", false, true},
		{"", "", false, true},
	}
	for _, tt := range tests {
		t.Run(tt.mamp, func(t *testing.T) {
			got, warnings, err := importPHP(tt.mamp, available)
			if (err != nil) != tt.wantErr || got != tt.want || (len(warnings) > 0) != tt.warn {
				t.Fatalf("got %q %v %v", got, warnings, err)
			}
		})
	}
}

func TestSiteNameLength(t *testing.T) {
	long := ""
	for range 20 {
		long += "abcd."
	}
	if n := siteName(long + "x"); len(n) > 63 || !siteNameRe.MatchString(n) {
		t.Fatalf("siteName = %q", n)
	}
}
