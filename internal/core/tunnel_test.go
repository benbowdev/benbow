// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
)

func TestTunnelHost(t *testing.T) {
	for h, want := range map[string]bool{
		"wishes-thunder-logos.trycloudflare.com": true,
		"trycloudflare.com":                      true,
		"":                                       false,
		"localhost":                              false,
		"a b.trycloudflare.com":                  false,
		"x.trycloudflare.com/../":                false,
		"-x.trycloudflare.com":                   false,
		`x.com"{}`:                               false,
	} {
		if got := tunnelHost(h); got != want {
			t.Errorf("tunnelHost(%q) = %v", h, got)
		}
	}
}

func TestNewTunnelPassword(t *testing.T) {
	re := regexp.MustCompile(`^[a-z2-9]{4}(-[a-z2-9]{4}){3}$`)
	a, b := newTunnelPassword(), newTunnelPassword()
	if !re.MatchString(a) || a == b || strings.ContainsAny(a, "ilo01") {
		t.Fatalf("passwords %q, %q", a, b)
	}
	h, err := bcrypt.GenerateFromPassword([]byte(a), bcrypt.MinCost)
	if err != nil || bcrypt.CompareHashAndPassword(h, []byte(a)) != nil {
		t.Fatal(err)
	}
}

func TestCloudflaredConfig(t *testing.T) {
	got := cloudflaredConfig("/Users/x/Library/Application Support/benbow/run/tunnel.sock")
	for _, want := range []string{"no-autoupdate: true", `  - service: "unix:/Users/x/Library/Application Support/benbow/run/tunnel.sock"`} {
		if !strings.Contains(got, want) {
			t.Errorf("config lacks %q:\n%s", want, got)
		}
	}
	args := cloudflaredArgs("/etc/cloudflared.yml", 20241)
	for _, want := range []string{"--no-autoupdate", "127.0.0.1:20241", "/etc/cloudflared.yml", "--url"} {
		if !slices.Contains(args, want) {
			t.Errorf("args lack %q: %v", want, args)
		}
	}
}

// fakeMetrics serves cloudflared's /quicktunnel and /ready on 127.0.0.1.
func fakeMetrics(t *testing.T, host string, ready int) int {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/quicktunnel":
			_ = json.NewEncoder(w).Encode(map[string]string{"hostname": host})
		case "/ready":
			w.WriteHeader(ready)
			_ = json.NewEncoder(w).Encode(map[string]int{"status": ready, "readyConnections": 1})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.Listener.Addr().(*net.TCPAddr).Port
}

func TestTunnelStatus(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	resolves := false
	old := tunnelResolvesFunc
	tunnelResolvesFunc = func(context.Context, string) bool { return resolves }
	t.Cleanup(func() { tunnelResolvesFunc = old })

	share := &TunnelShare{Site: "shop", Until: time.Now().Add(30 * time.Minute), MetricsPort: fakeMetrics(t, "abc-def.trycloudflare.com", http.StatusOK)}
	st, err := c.tunnelStatus(ctx, share)
	if err != nil || st.URL != "https://abc-def.trycloudflare.com" || st.Ready || st.Minutes != 30 || st.Protected {
		t.Fatalf("status before DNS = %+v, %v", st, err)
	}
	resolves = true
	if st, _ = c.tunnelStatus(ctx, share); !st.Ready {
		t.Fatalf("status once in DNS = %+v", st)
	}
	// Remembered: not asked again.
	resolves = false
	if st, _ = c.tunnelStatus(ctx, share); !st.Ready {
		t.Fatal("resolved name forgotten")
	}

	share.MetricsPort = fakeMetrics(t, "ghi.trycloudflare.com", http.StatusServiceUnavailable)
	resolves = true
	if st, _ = c.tunnelStatus(ctx, share); st.URL == "" || st.Ready {
		t.Fatalf("not connected yet = %+v", st)
	}
	share.MetricsPort = fakeMetrics(t, `evil.com" onload="x`, http.StatusOK)
	if st, _ = c.tunnelStatus(ctx, share); st.URL != "" || st.Ready {
		t.Fatalf("bad name accepted = %+v", st)
	}
	share.MetricsPort = 1 // nothing listens
	share.PasswordHash = "$2a$10$x"
	if st, _ = c.tunnelStatus(ctx, share); st.URL != "" || st.Ready || !st.Protected {
		t.Fatalf("cloudflared down = %+v", st)
	}
}

func TestExpireTunnel(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	b, _ := json.Marshal(TunnelShare{Site: "shop", MetricsPort: 20241, Until: time.Now().Add(time.Hour)})
	if err := c.Store.SetSetting(ctx, settingTunnel, string(b)); err != nil {
		t.Fatal(err)
	}
	if done, err := c.ExpireTunnel(ctx); err != nil || done {
		t.Fatalf("expire before time = %v, %v", done, err)
	}
	b, _ = json.Marshal(TunnelShare{Site: "shop", Until: time.Now().Add(-time.Second)})
	if err := c.Store.SetSetting(ctx, settingTunnel, string(b)); err != nil {
		t.Fatal(err)
	}
	if share, err := c.Tunnel(ctx); err != nil || share != nil {
		t.Fatalf("expired link still reported: %+v, %v", share, err)
	}
	if done, err := c.ExpireTunnel(ctx); err != nil || !done {
		t.Fatalf("expire after time = %v, %v", done, err)
	}
	if _, ok, _ := c.Store.Setting(ctx, settingTunnel); ok {
		t.Fatal("link not removed")
	}
}

func TestOpenTunnelRefuses(t *testing.T) {
	ctx := context.Background()
	c := groupsCore(t)
	if err := c.Store.AddSite(ctx, store.Site{Name: "off", Path: t.TempDir(), PHPMinor: "8.4", Domain: "off.localhost"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Store.AddSite(ctx, store.Site{Name: "shop", Path: t.TempDir(), PHPMinor: "8.4", Domain: "shop.localhost", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.OpenTunnel(ctx, "off", 0, false); errcode.Code(err) != errcode.Conflict || !strings.Contains(err.Error(), "turn it on") {
		t.Errorf("site off: %v", err)
	}
	if _, err := c.OpenTunnel(ctx, "shop", 0, false); errcode.Code(err) != errcode.Conflict {
		t.Errorf("Benbow stopped: %v", err)
	}
	if _, err := c.OpenTunnel(ctx, "nope", 10, false); errcode.Code(err) != errcode.NotFound {
		t.Errorf("unknown site: %v", err)
	}
	for _, m := range []int{-1, tunnelMaxMinutes + 1} {
		if _, err := c.OpenTunnel(ctx, "shop", m, false); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("minutes %d: %v", m, err)
		}
	}
	if _, ok, _ := c.Store.Setting(ctx, settingTunnel); ok {
		t.Fatal("a refused link was stored")
	}
}
