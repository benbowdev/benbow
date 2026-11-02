// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestAliases serves a site under extra names and keeps every name unique.
func TestAliases(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	h.mustCovo("site", "add", "other", "--php", "8.4", "--path", newSite(t))
	h.mustCovo("start")

	var res struct {
		Aliases []string `json:"aliases"`
	}
	data := h.mustCovo("site", "set", "demo", "--aliases", "www.demo.localhost,Shop.localhost, www.demo.localhost")
	if err := json.Unmarshal(data, &res); err != nil || strings.Join(res.Aliases, ",") != "shop.localhost,www.demo.localhost" {
		t.Fatalf("aliases = %v, %v", res.Aliases, err)
	}
	st := h.status()
	for _, host := range []string{"demo.localhost", "www.demo.localhost", "shop.localhost"} {
		url := "https://" + host + ":" + strconv.Itoa(st.HTTPSPort)
		if body := h.get(url, st.CA.CertPath); !strings.HasPrefix(body, "8.4.") {
			t.Errorf("%s served %q", url, body)
		}
	}

	for _, tt := range []struct {
		args []string
		code string
	}{
		{[]string{"site", "set", "other", "--aliases", "shop.localhost"}, "conflict"},
		{[]string{"site", "set", "other", "--aliases", "demo.localhost"}, "conflict"},
		{[]string{"site", "set", "other", "--domain", "www.demo.localhost"}, "conflict"},
		{[]string{"site", "add", "third", "--domain", "shop.localhost", "--path", newSite(t)}, "conflict"},
		{[]string{"site", "set", "demo", "--aliases", "demo.localhost"}, "invalid_input"},
		{[]string{"site", "set", "demo", "--aliases", "demo.example.com"}, "invalid_input"},
		{[]string{"site", "set", "demo", "--aliases", "demo.test"}, "unsupported"},
	} {
		if env := h.covo(tt.args...); env.OK || env.Error.Code != tt.code {
			t.Errorf("%v = %+v, want %s", tt.args, env, tt.code)
		}
	}

	data = h.mustCovo("site", "set", "demo", "--aliases", "")
	if err := json.Unmarshal(data, &res); err != nil || len(res.Aliases) != 0 {
		t.Fatalf("aliases after clearing = %v, %v", res.Aliases, err)
	}
	// The freed name can now be used elsewhere.
	h.mustCovo("site", "set", "other", "--aliases", "shop.localhost")
}
