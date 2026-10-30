// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type daemonData struct {
	Label     string `json:"label"`
	Socket    string `json:"socket"`
	Loaded    bool   `json:"loaded"`
	PID       int    `json:"pid"`
	Answering bool   `json:"answering"`
	Action    string `json:"action"`
}

func (h *harness) daemon(args ...string) daemonData {
	h.t.Helper()
	var d daemonData
	if err := json.Unmarshal(h.mustCovo(append([]string{"daemon"}, args...)...), &d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

// api calls the daemon over its socket and returns the decoded envelope.
func (h *harness) api(sock, method, path, body string) envelope {
	h.t.Helper()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, "http://covo"+path, strings.NewReader(body))
	if err != nil {
		h.t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	var env envelope
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		h.t.Fatalf("%s %s: %v", method, path, err)
	}
	return env
}

func (h *harness) mustAPI(sock, method, path, body string) json.RawMessage {
	h.t.Helper()
	env := h.api(sock, method, path, body)
	if !env.OK {
		h.t.Fatalf("%s %s: %+v", method, path, env.Error)
	}
	return env.Data
}

// TestDaemon drives Covo through the API the app uses: the daemon serves
// the core, survives start/stop of the services and goes with uninstall.
func TestDaemon(t *testing.T) {
	h := newHarness(t)
	if d := h.daemon(); d.Loaded || d.Answering {
		t.Fatalf("daemon running before start: %+v", d)
	}

	d := h.daemon("start")
	if d.Action != "started" || !d.Answering || d.PID == 0 {
		t.Fatalf("start: %+v", d)
	}
	sock, pid := d.Socket, d.PID
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 || fi.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket %s: %v %v", sock, fi.Mode(), err)
	}
	if d := h.daemon("start"); d.Action != "unchanged" || d.PID != pid {
		t.Fatalf("second start: %+v", d)
	}

	// The daemon sees the test's home and overrides, not the real ones.
	var st statusData
	if err := json.Unmarshal(h.mustAPI(sock, "GET", "/v1/status", ""), &st); err != nil {
		t.Fatal(err)
	}
	if st.Home != h.home() {
		t.Fatalf("daemon home = %q, want %q", st.Home, h.home())
	}

	// Start, add a site, serve it: all through the API.
	h.mustAPI(sock, "POST", "/v1/start", "")
	var added struct {
		URL string `json:"url"`
	}
	body, _ := json.Marshal(map[string]any{"name": "api", "php": "8.4", "path": newSite(t)})
	if err := json.Unmarshal(h.mustAPI(sock, "POST", "/v1/sites", string(body)), &added); err != nil {
		t.Fatal(err)
	}
	if got := h.get(added.URL, h.status().CA.CertPath); !strings.HasPrefix(got, "8.4.") {
		t.Fatalf("body = %q", got)
	}
	if env := h.api(sock, "POST", "/v1/sites", string(body)); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("duplicate site: %+v", env.Error)
	}
	if st := h.status(); len(st.Orphans) != 0 {
		t.Fatalf("the daemon shows up as an orphan: %v", st.Orphans)
	}

	// Stopping the services, through the API or the CLI, leaves the daemon.
	h.mustAPI(sock, "POST", "/v1/stop", "")
	h.mustCovo("stop")
	if d := h.daemon(); !d.Answering || d.PID != pid {
		t.Fatalf("stop took the daemon down: %+v", d)
	}
	if labels := h.loadedLabels(); !slices.Equal(labels, []string{d.Label}) {
		t.Fatalf("loaded after stop: %v", labels)
	}

	// A different covo binary restarts it.
	other := filepath.Join(t.TempDir(), "covo")
	if out, err := exec.Command("cp", h.bin, other).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	cmd := exec.Command(other, "daemon", "start", "--json")
	cmd.Env = h.env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("start from another binary: %v %s", err, out)
	}
	var env struct {
		Data daemonData `json:"data"`
	}
	if err := json.Unmarshal(out, &env); err != nil || env.Data.Action != "restarted" || env.Data.PID == pid || !env.Data.Answering {
		t.Fatalf("restart: %+v %v", env.Data, err)
	}

	// Stop, then uninstall takes it down too.
	if d := h.daemon("stop"); d.Action != "stopped" || d.Loaded || d.Answering {
		t.Fatalf("stop: %+v", d)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("socket left behind: %v", err)
	}
	h.daemon("start")
	h.mustCovo("uninstall", "--yes")
	if labels := h.loadedLabels(); len(labels) != 0 {
		t.Fatalf("loaded after uninstall: %v", labels)
	}
}
