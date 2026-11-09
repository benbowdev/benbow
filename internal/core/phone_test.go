// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"
	"time"
)

func TestPhoneHost(t *testing.T) {
	for _, tt := range []struct{ site, mac, want string }{
		{"shop", "andreas-macbook-pro", "shop-andreas-macbook-pro.local"},
		{strings.Repeat("a", 60), "mac", strings.Repeat("a", 60) + "-ma.local"},
		{strings.Repeat("a", 62), "mac", strings.Repeat("a", 62) + ".local"},
	} {
		if got := phoneHost(tt.site, tt.mac); got != tt.want {
			t.Errorf("phoneHost(%s, %s) = %s", tt.site, tt.mac, got)
		}
	}
	if m := macName(); m == "" || strings.ContainsAny(m, " .A") {
		t.Errorf("macName = %q", m)
	}
}

func TestMobileconfig(t *testing.T) {
	out := mobileconfig([]byte{1, 2, 3}, strings.Repeat("ab", 32))
	var v struct{ XMLName xml.Name }
	if err := xml.Unmarshal([]byte(out), &v); err != nil || v.XMLName.Local != "plist" {
		t.Fatalf("not a plist: %v", err)
	}
	for _, want := range []string{"com.apple.security.root", "<data>AQID</data>", "Benbow Local CA"} {
		if !strings.Contains(out, want) {
			t.Errorf("profile lacks %q", want)
		}
	}
}

func TestExpirePhone(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	b, _ := json.Marshal(PhoneShare{Site: "shop", Host: "shop-mac.local", Port: 9443, Until: time.Now().Add(time.Hour)})
	if err := c.Store.SetSetting(ctx, settingPhone, string(b)); err != nil {
		t.Fatal(err)
	}
	if done, err := c.ExpirePhone(ctx); err != nil || done {
		t.Fatalf("expire before time = %v, %v", done, err)
	}
	b, _ = json.Marshal(PhoneShare{Site: "shop", Until: time.Now().Add(-time.Second)})
	if err := c.Store.SetSetting(ctx, settingPhone, string(b)); err != nil {
		t.Fatal(err)
	}
	if share, err := c.phoneShare(ctx); err != nil || share != nil {
		t.Fatalf("expired share still reported: %+v, %v", share, err)
	}
	if done, err := c.ExpirePhone(ctx); err != nil || !done {
		t.Fatalf("expire after time = %v, %v", done, err)
	}
	if _, ok, _ := c.Store.Setting(ctx, settingPhone); ok {
		t.Fatal("share not removed")
	}
}
