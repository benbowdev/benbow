// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeCloudflared stands in for cloudflared: it reads the socket from
// Benbow's config, answers /quicktunnel and /ready on --metrics like the
// real one, and forwards /proxy/... to the socket the way Cloudflare's edge
// would (public Host, X-Forwarded-Proto https). It writes its arguments
// and HOME to $HOME/fake.json.
const fakeCloudflared = `package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httputil"
	"os"
	"regexp"
	"strconv"
	"strings"
)

const host = "benbow-e2e.trycloudflare.com"

func main() {
	var conf, metrics string
	for i, a := range os.Args {
		if i+1 < len(os.Args) {
			switch a {
			case "--config":
				conf = os.Args[i+1]
			case "--metrics":
				metrics = os.Args[i+1]
			}
		}
	}
	b, err := os.ReadFile(conf)
	if err != nil {
		panic(err)
	}
	m := regexp.MustCompile(` + "`" + `service: ("[^"]+")` + "`" + `).FindSubmatch(b)
	if m == nil {
		panic("no service in " + conf)
	}
	svc, _ := strconv.Unquote(string(m[1]))
	sock := strings.TrimPrefix(svc, "unix:")
	rec, _ := json.Marshal(map[string]any{"args": os.Args[1:], "home": os.Getenv("HOME"), "socket": sock, "metrics": metrics})
	_ = os.WriteFile(os.Getenv("HOME")+"/fake.json", rec, 0o600)
	proxy := &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.Out.URL.Scheme, r.Out.URL.Host = "http", host
			r.Out.URL.Path = strings.TrimPrefix(r.In.URL.Path, "/proxy")
			r.Out.Host = host
			r.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		}},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/quicktunnel", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"hostname": host})
	})
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]int{"status": 200, "readyConnections": 1})
	})
	mux.Handle("/proxy/", proxy)
	panic(http.ListenAndServe(metrics, mux))
}
`

const tunnelPage = `<?php
echo defined('WP_HOME') ? WP_HOME : 'none', '|', $_SERVER['HTTPS'] ?? 'off', '|', $_SERVER['BENBOW_LOCAL_URL'] ?? 'local', '/x';
`

// TestTunnel gives a site a public link through a fake cloudflared served
// from a signed test manifest: Benbow's own config and HOME, the socket
// path, the URL rewrite, hidden files, the password, more time, closing,
// and a stop that does not bring the link back.
func TestTunnel(t *testing.T) {
	work := t.TempDir()
	src := filepath.Join(work, "main.go")
	if err := os.WriteFile(src, []byte(fakeCloudflared), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "go.mod"), []byte("module fakecloudflared\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(work, "cloudflared")
	build := exec.Command("go", "build", "-o", bin, ".")
	build.Dir = work
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fake cloudflared: %v\n%s", err, out)
	}
	exe, err := os.ReadFile(bin)
	if err != nil {
		t.Fatal(err)
	}
	var tgz bytes.Buffer
	gz := gzip.NewWriter(&tgz)
	tw := tar.NewWriter(gz)
	_ = tw.WriteHeader(&tar.Header{Name: "cloudflared", Mode: 0o755, Size: int64(len(exe)), Typeflag: tar.TypeReg})
	_, _ = tw.Write(exe)
	_ = tw.Close()
	_ = gz.Close()
	sum := sha256.Sum256(tgz.Bytes())

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	srv := &manifestServer{}
	mux := http.NewServeMux()
	mux.Handle("/", srv)
	mux.HandleFunc("/cloudflared.tgz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(tgz.Bytes()) })
	ts := httptest.NewServer(mux)
	defer ts.Close()
	var embedded struct {
		Serial int64 `json:"serial"`
	}
	raw, err := os.ReadFile("../../internal/components/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &embedded)
	manifest, _ := json.Marshal(map[string]any{
		"serial": embedded.Serial + 1, "expires": time.Now().Add(14 * 24 * time.Hour).UTC().Format(time.RFC3339),
		"artifacts": []map[string]any{{
			"component": "cloudflared", "version": "2099.1.1", "arch": runtime.GOARCH, "url": ts.URL + "/cloudflared.tgz",
			"sha256": hex.EncodeToString(sum[:]), "files": []string{"cloudflared"},
		}},
	})
	srv.set(manifest, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, manifest))))

	h := newHarness(t)
	h.env = append(h.env, "BENBOW_MANIFEST_URL="+ts.URL+"/manifest.json",
		"BENBOW_MANIFEST_RELEASE_URL="+ts.URL+"/release/manifest.json",
		"BENBOW_MANIFEST_TEST_KEY="+base64.StdEncoding.EncodeToString(pub),
		"BENBOW_TEST_TUNNEL_NO_DNS=1")
	h.mustBenbow("update", "check")
	var home string
	for _, e := range h.env {
		if v, ok := strings.CutPrefix(e, "BENBOW_HOME="); ok {
			home = v
		}
	}

	dir := newSite(t)
	for name, body := range map[string]string{"index.php": tunnelPage, ".env": "SECRET=1", "dump.sql": "--"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.mustBenbow("site", "add", "hooks", "--php", "8.4", "--path", dir)
	if env := h.benbow("tunnel", "hooks"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("tunnel while stopped = %+v", env)
	}
	h.mustBenbow("start")

	type status struct {
		Site      string `json:"site"`
		URL       string `json:"url"`
		Ready     bool   `json:"ready"`
		Minutes   int    `json:"minutes_left"`
		Protected bool   `json:"protected"`
		User      string `json:"user"`
		Password  string `json:"password"`
		LocalURL  string `json:"local_url"`
	}
	var tun status
	if err := json.Unmarshal(h.mustBenbow("tunnel", "hooks", "--minutes", "5"), &tun); err != nil ||
		tun.URL != "https://benbow-e2e.trycloudflare.com" || !tun.Ready || tun.Minutes < 4 || tun.Protected || tun.Password != "" ||
		!strings.Contains(tun.LocalURL, "hooks.localhost") {
		t.Fatalf("tunnel = %+v, %v", tun, err)
	}

	var fake struct {
		Args    []string `json:"args"`
		Home    string   `json:"home"`
		Socket  string   `json:"socket"`
		Metrics string   `json:"metrics"`
	}
	b, err := os.ReadFile(filepath.Join(home, "data", "tunnel", "home", "fake.json"))
	if err != nil || json.Unmarshal(b, &fake) != nil {
		t.Fatalf("fake cloudflared record: %v", err)
	}
	args := strings.Join(fake.Args, " ")
	if fake.Home != filepath.Join(home, "data", "tunnel", "home") || !strings.Contains(args, "--no-autoupdate") ||
		!strings.Contains(args, "--config "+filepath.Join(home, "etc", "cloudflared.yml")) || !strings.HasPrefix(fake.Metrics, "127.0.0.1:") ||
		!strings.HasSuffix(fake.Socket, "/tunnel.sock") {
		t.Fatalf("cloudflared ran with %+v", fake)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	get := func(path, user, pass string) (int, string, http.Header) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, "http://"+fake.Metrics+"/proxy"+path, nil)
		if user != "" {
			req.SetBasicAuth(user, pass)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body), resp.Header
	}
	code, body, hdr := get("/", "", "")
	if want := "https://benbow-e2e.trycloudflare.com|on|https://benbow-e2e.trycloudflare.com/x"; code != 200 || body != want {
		t.Fatalf("page through the link = %d %q (want %q)", code, body, want)
	}
	if hdr.Get("X-Robots-Tag") == "" {
		t.Error("no X-Robots-Tag on the public link")
	}
	for _, p := range []string{"/.env", "/dump.sql"} {
		if code, _, _ := get(p, "", ""); code != http.StatusNotFound {
			t.Errorf("%s through the link = %d", p, code)
		}
	}
	// The site's own address is unchanged.
	st := h.status()
	if local := h.get(tun.LocalURL+"/", st.CA.CertPath); local != "none|on|local/x" {
		t.Errorf("local page = %q", local)
	}
	if !strings.Contains(string(h.mustBenbow("status")), `"tunnel"`) {
		t.Error("status does not list the public link")
	}

	// A password: shown once, asked of visitors, kept when the link gets
	// more time.
	if err := json.Unmarshal(h.mustBenbow("tunnel", "hooks", "--password"), &tun); err != nil || !tun.Protected || tun.User != "benbow" || len(tun.Password) != 19 {
		t.Fatalf("tunnel --password = %+v, %v", tun, err)
	}
	pass := tun.Password
	if code, _, _ := get("/", "", ""); code != http.StatusUnauthorized {
		t.Errorf("no password = %d", code)
	}
	if code, _, _ := get("/", "benbow", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong password = %d", code)
	}
	if code, _, _ := get("/", "benbow", pass); code != 200 {
		t.Errorf("right password = %d", code)
	}
	tun = status{}
	if err := json.Unmarshal(h.mustBenbow("tunnel", "hooks", "--password", "--minutes", "120"), &tun); err != nil || !tun.Protected || tun.Password != "" || tun.Minutes < 119 {
		t.Fatalf("more time = %+v, %v", tun, err)
	}
	if code, _, _ := get("/", "benbow", pass); code != 200 {
		t.Errorf("password after more time = %d", code)
	}
	tun = status{}
	if err := json.Unmarshal(h.mustBenbow("tunnel", "hooks"), &tun); err != nil || tun.Protected {
		t.Fatalf("password off = %+v, %v", tun, err)
	}
	if code, _, _ := get("/", "", ""); code != 200 {
		t.Errorf("after the password was turned off = %d", code)
	}

	// Another site gets a new tunnel (here: a new metrics port), never the
	// old address.
	other := newSite(t)
	h.mustBenbow("site", "add", "other", "--php", "8.4", "--path", other)
	h.mustBenbow("tunnel", "other")
	b, _ = os.ReadFile(filepath.Join(home, "data", "tunnel", "home", "fake.json"))
	var moved struct {
		Metrics string `json:"metrics"`
	}
	if json.Unmarshal(b, &moved) != nil || moved.Metrics == fake.Metrics {
		t.Fatalf("another site kept the tunnel on %s", moved.Metrics)
	}
	fake.Metrics = moved.Metrics

	h.mustBenbow("tunnel", "--off")
	if out := strings.TrimSpace(string(h.mustBenbow("tunnel"))); out != "{}" && out != "null" {
		t.Fatalf("after --off = %s", out)
	}
	if _, err := client.Get("http://" + fake.Metrics + "/ready"); err == nil {
		t.Error("cloudflared still runs after --off")
	}

	// Stopping Benbow closes the link for good.
	h.mustBenbow("tunnel", "hooks")
	h.mustBenbow("stop")
	h.mustBenbow("start")
	if out := strings.TrimSpace(string(h.mustBenbow("tunnel"))); out != "{}" && out != "null" {
		t.Fatalf("link back after stop and start = %s", out)
	}
}
