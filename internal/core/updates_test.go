// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/version"
)

func TestDueForAutoCheck(t *testing.T) {
	now := time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		last time.Time
		want bool
	}{
		{"never checked", time.Time{}, true},
		{"an hour ago", now.Add(-time.Hour), false},
		{"23 hours ago", now.Add(-23 * time.Hour), false},
		{"24 hours ago", now.Add(-24 * time.Hour), true},
		{"three days ago", now.Add(-72 * time.Hour), true},
		{"clock went back a day", now.Add(24 * time.Hour), true},
		{"a few minutes ahead", now.Add(5 * time.Minute), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dueForAutoCheck(tt.last, now); got != tt.want {
				t.Errorf("dueForAutoCheck = %v, want %v", got, tt.want)
			}
		})
	}
}

// manifestServer serves a signed manifest at /good, a wrong signature at
// /forged, an expired one at /old and 404 elsewhere; it records requests.
func manifestServer(t *testing.T, priv ed25519.PrivateKey) (*httptest.Server, func() []string) {
	t.Helper()
	sign := func(expires time.Time, key ed25519.PrivateKey) ([]byte, []byte) {
		body, _ := json.Marshal(map[string]any{"serial": 7, "expires": expires.UTC().Format(time.RFC3339), "artifacts": []any{}})
		return body, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, body)))
	}
	_, other, _ := ed25519.GenerateKey(nil)
	files := map[string][]byte{}
	files["/good"], files["/good.sig"] = sign(time.Now().Add(14*24*time.Hour), priv)
	files["/forged"], files["/forged.sig"] = sign(time.Now().Add(14*24*time.Hour), other)
	files["/old"], files["/old.sig"] = sign(time.Now().Add(-time.Hour), priv)
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.Path+" "+r.UserAgent())
		mu.Unlock()
		if b, ok := files[r.URL.Path]; ok {
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), seen...) }
}

func TestFirstVerifiedFallsBack(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	keys := []ed25519.PublicKey{pub}
	srv, seen := manifestServer(t, priv)
	down := "http://127.0.0.1:1/manifest.json" // nothing listens there
	tests := []struct {
		name    string
		urls    []string
		wantErr bool
	}{
		{"release works", []string{"/good"}, false},
		{"release missing (being replaced)", []string{"/missing", "/good"}, false},
		{"release forged", []string{"/forged", "/good"}, false},
		{"release expired", []string{"/old", "/good"}, false},
		{"network down", []string{down, "/good"}, false},
		{"both fail", []string{"/missing", "/forged"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var urls []string
			for _, u := range tt.urls {
				if strings.HasPrefix(u, "/") {
					u = srv.URL + u
				}
				urls = append(urls, u)
			}
			m, _, _, err := firstVerified(context.Background(), urls, keys)
			if (err != nil) != tt.wantErr {
				t.Fatalf("firstVerified = %v", err)
			}
			if err == nil && m.Serial != 7 {
				t.Fatalf("serial = %d", m.Serial)
			}
		})
	}
	for _, s := range seen() {
		if !strings.HasSuffix(s, " "+version.UserAgent()) {
			t.Fatalf("request without Covo's User-Agent: %q", s)
		}
	}
}
