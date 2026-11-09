// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type updates struct {
	Serial  int64 `json:"serial"`
	Updates []struct {
		Component string `json:"component"`
		Current   string `json:"current"`
		Available string `json:"available"`
	} `json:"updates"`
	Applied []struct {
		Component string `json:"component"`
	} `json:"applied"`
	Live bool `json:"live"`
	Auto bool `json:"auto"`
}

func (h *harness) updates(args ...string) updates {
	h.t.Helper()
	var u updates
	if err := json.Unmarshal(h.mustBenbow(append([]string{"update"}, args...)...), &u); err != nil {
		h.t.Fatal(err)
	}
	return u
}

func (u updates) has(component, available string) bool {
	for _, x := range u.Updates {
		if x.Component == component && x.Available == available {
			return true
		}
	}
	return false
}

// manifestServer serves a manifest and its signature, which the test can
// change between requests.
type manifestServer struct {
	mu        sync.Mutex
	body, sig []byte
	// release: the copy at /release/ (the release asset) is served;
	// releaseHits counts requests for it.
	release     bool
	releaseHits int
}

func (s *manifestServer) setRelease(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.release = on
}

func (s *manifestServer) hits() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releaseHits
}

func (s *manifestServer) set(body, sig []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.body, s.sig = body, sig
}

func (s *manifestServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	path := r.URL.Path
	if p, ok := strings.CutPrefix(path, "/release"); ok {
		s.releaseHits++
		if !s.release {
			http.NotFound(w, r) // being replaced by the workflow
			return
		}
		path = p
	}
	switch path {
	case "/manifest.json":
		_, _ = w.Write(s.body)
	case "/manifest.json.sig":
		_, _ = w.Write(s.sig)
	default:
		http.NotFound(w, r)
	}
}

// TestUpdates publishes a signed manifest with a "new" Mailpit (the same
// pinned binary under a higher version) and a Caddy and a PHP whose
// binaries do not report the version they claim, then applies, refuses
// and rolls back.
func TestUpdates(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("the test manifest is built from the arm64 entries")
	}
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := &manifestServer{}
	ts := httptest.NewServer(srv)
	defer ts.Close()

	h := newHarness(t)
	h.env = append(h.env, "BENBOW_MANIFEST_URL="+ts.URL+"/manifest.json",
		"BENBOW_MANIFEST_RELEASE_URL="+ts.URL+"/release/manifest.json",
		"BENBOW_MANIFEST_TEST_KEY="+base64.StdEncoding.EncodeToString(pub))
	h.mustBenbow("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	h.mustBenbow("mail", "enable")
	h.mustBenbow("start")

	// The embedded manifest, plus two versions that do not exist upstream.
	raw, err := os.ReadFile("../../internal/components/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var embedded struct {
		Serial    int64            `json:"serial"`
		Artifacts []map[string]any `json:"artifacts"`
	}
	if err := json.Unmarshal(raw, &embedded); err != nil {
		t.Fatal(err)
	}
	var extra []map[string]any
	for _, a := range embedded.Artifacts {
		if a["arch"] != "arm64" {
			continue
		}
		switch {
		case a["component"] == "mailpit" && a["version"] == "1.33.0":
			b := clone(a)
			b["version"] = "1.33.99"
			extra = append(extra, b)
		case a["component"] == "caddy" && a["version"] == "2.11.7":
			b := clone(a) // caddy still says v2.11.7: Benbow must refuse it
			b["version"] = "2.11.99"
			extra = append(extra, b)
		case a["component"] == "php" && a["version"] == "8.4.23":
			b := clone(a) // php-fpm still says 8.4.23: Benbow must refuse it
			b["version"] = "8.4.99"
			extra = append(extra, b)
		}
	}
	expires := time.Now().Add(14 * 24 * time.Hour).UTC().Format(time.RFC3339)
	publish := func(serial int64, key ed25519.PrivateKey) {
		body, _ := json.Marshal(map[string]any{"serial": serial, "expires": expires, "artifacts": extra})
		srv.set(body, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(key, body))))
	}

	// Signed by another key: refused, nothing offered.
	_, other, _ := ed25519.GenerateKey(rand.Reader)
	publish(embedded.Serial+1, other)
	if env := h.benbow("update", "check"); env.OK || env.Error.Code != "checksum_mismatch" {
		t.Fatalf("check with a foreign signature = %+v", env)
	}
	if u := h.updates(); len(u.Updates) != 0 {
		t.Fatalf("updates after a refused manifest: %+v", u.Updates)
	}

	// Properly signed but expired: refused (a stuck or replayed server).
	expires = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	publish(embedded.Serial+1, priv)
	if env := h.benbow("update", "check"); env.OK || env.Error.Code != "checksum_mismatch" || !strings.Contains(env.Error.Message, "expired") {
		t.Fatalf("check with an expired manifest = %+v", env)
	}
	expires = time.Now().Add(14 * 24 * time.Hour).UTC().Format(time.RFC3339)

	publish(embedded.Serial+1, priv)
	u := h.updates("check")
	if u.Serial != embedded.Serial+1 || !u.has("mailpit", "1.33.99") || !u.has("caddy", "2.11.99") || !u.has("php-8.4", "8.4.99") {
		t.Fatalf("check = %+v", u)
	}

	// Manual checks never ask the release asset (it counts daily checks);
	// the automatic one falls back to main while the asset is missing,
	// then takes it from the release.
	if n := srv.hits(); n != 0 {
		t.Fatalf("manual check asked the release asset %d times", n)
	}
	if u := h.updates("check", "--auto"); u.Serial != embedded.Serial+1 || !u.Auto {
		t.Fatalf("automatic check with the asset missing = %+v", u)
	}
	if srv.hits() == 0 {
		t.Fatal("the automatic check did not try the release asset")
	}
	srv.setRelease(true)
	before := srv.hits()
	if env := h.benbow("update", "check", "--auto"); !env.OK {
		t.Fatalf("automatic check from the release = %+v", env)
	}
	if got := srv.hits() - before; got != 2 {
		t.Fatalf("release requests = %d, want the manifest and its signature", got)
	}
	if u := h.updates("auto", "off"); u.Auto {
		t.Fatalf("update auto off = %+v", u)
	}
	if u := h.updates("auto", "on"); !u.Auto {
		t.Fatalf("update auto on = %+v", u)
	}

	plist := filepath.Join(homeOf(t, h), "launchd", h.prefix()+".mailpit.plist")
	u = h.updates("apply", "mailpit")
	if len(u.Applied) != 1 || !u.Live || u.has("mailpit", "1.33.99") {
		t.Fatalf("apply mailpit = %+v", u)
	}
	if b, _ := os.ReadFile(plist); !strings.Contains(string(b), "1.33.99") {
		t.Fatal("Mailpit's agent does not run the new version")
	}
	for _, svc := range h.status().Services {
		if svc.Name == "mailpit" && !svc.Running {
			t.Fatal("mailpit not running after the update")
		}
	}

	// Binaries that do not report the version they claim are refused, and
	// nothing changes.
	if env := h.benbow("update", "apply", "caddy"); env.OK || env.Error.Code != "checksum_mismatch" {
		t.Fatalf("apply broken caddy = %+v", env)
	}
	st := h.status()

	// A PHP binary that does not report the version it claims: refused,
	// and the site keeps its PHP.
	if env := h.benbow("update", "apply", "php-8.4"); env.OK || env.Error.Code != "checksum_mismatch" {
		t.Fatalf("apply broken php = %+v", env)
	}
	if body := h.get(st.Sites[0].URL, st.CA.CertPath); body != "8.4.23" {
		t.Fatalf("site after the refused PHP update: %q", body)
	}

	// An older manifest is never taken.
	publish(embedded.Serial, priv)
	if u := h.updates("check"); u.Serial != embedded.Serial+1 {
		t.Fatalf("serial went back to %d", u.Serial)
	}

	u = h.updates("rollback", "mailpit")
	if !u.has("mailpit", "1.33.99") {
		t.Fatalf("after rollback mailpit should be offered again: %+v", u)
	}
	if b, _ := os.ReadFile(plist); strings.Contains(string(b), "1.33.99") {
		t.Fatal("Mailpit still runs the new version after rollback")
	}
}

func clone(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}
