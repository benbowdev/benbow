// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"bufio"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// devServer stands in for a dev server: it listens on ::1 only (like Node
// resolving "localhost" to ::1), reports the headers it got, and echoes the
// bytes of a WebSocket connection (like a hot-reload channel).
func devServer(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "[::1]:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			key := r.Header.Get("Sec-WebSocket-Key")
			sum := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
			conn, rw, err := w.(http.Hijacker).Hijack()
			if err != nil {
				return
			}
			defer conn.Close()
			fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n",
				base64.StdEncoding.EncodeToString(sum[:]))
			_ = rw.Flush()
			_, _ = io.Copy(conn, rw) // echo until the client hangs up
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"host": r.Host, "proto": r.Header.Get("X-Forwarded-Proto"), "fwd_host": r.Header.Get("X-Forwarded-Host"), "path": r.URL.Path,
		})
	})}
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return l.Addr().(*net.TCPAddr).Port
}

// TestAppProxy serves a dev server the user runs behind a Covo domain:
// HTTPS with the local CA, forwarded headers, the Host option and a
// WebSocket upgrade (hot reload) through Caddy.
func TestAppProxy(t *testing.T) {
	h := newHarness(t)
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "package.json"), []byte(`{"scripts":{"dev":"vite"},"devDependencies":{"vite":"7"}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var detected struct {
		Framework string `json:"framework"`
		Command   string `json:"command"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "detect", project), &detected); err != nil {
		t.Fatal(err)
	}
	if detected.Framework != "vite" || detected.Command != "npm run dev -- --port {port} --strictPort" {
		t.Fatalf("detect = %+v", detected)
	}

	h.mustCovo("start")
	port := devServer(t)
	var added struct {
		Kind    string `json:"kind"`
		AppMode string `json:"app_mode"`
		AppPort int    `json:"app_port"`
		PHP     string `json:"php"`
		URL     string `json:"url"`
		Applied bool   `json:"applied"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "add", "front", "--path", project, "--proxy-port", strconv.Itoa(port)), &added); err != nil {
		t.Fatal(err)
	}
	if added.Kind != "app" || added.AppMode != "proxy" || added.AppPort != port || added.PHP != "" || !added.Applied {
		t.Fatalf("added = %+v", added)
	}
	st := h.status()
	for _, svc := range st.Services {
		if strings.HasPrefix(svc.Name, "php-") {
			t.Errorf("an app site started %s", svc.Name)
		}
	}

	type seen struct{ Host, Proto, FwdHost, Path string }
	read := func() seen {
		t.Helper()
		var got struct {
			Host    string `json:"host"`
			Proto   string `json:"proto"`
			FwdHost string `json:"fwd_host"`
			Path    string `json:"path"`
		}
		if err := json.Unmarshal([]byte(h.get(added.URL+"/hello", st.CA.CertPath)), &got); err != nil {
			t.Fatal(err)
		}
		return seen{got.Host, got.Proto, got.FwdHost, got.Path}
	}
	domain := "front.localhost"
	if got := read(); got.Host != domain+":"+strconv.Itoa(h.port) || got.Proto != "https" || got.Path != "/hello" {
		t.Errorf("proxied request = %+v", got)
	}

	h.mustCovo("site", "set", "front", "--host-header", "localhost")
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(h.home(), "logs", "caddy.log"))
			t.Logf("caddy.log:\n%s", b[max(0, len(b)-3000):])
		}
	})
	if got := read(); got.Host != "localhost:"+strconv.Itoa(port) || got.FwdHost != domain+":"+strconv.Itoa(h.port) {
		t.Errorf("with Host localhost = %+v", got)
	}

	// A WebSocket upgrade goes through and stays open both ways.
	pem, err := os.ReadFile(st.CA.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	conn, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(h.port), &tls.Config{RootCAs: pool, ServerName: domain, NextProtos: []string{"http/1.1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	fmt.Fprintf(conn, "GET /hmr HTTP/1.1\r\nHost: %s:%d\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\nSec-WebSocket-Version: 13\r\n\r\n", domain, h.port)
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %v, %v", resp, err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, 4)
	if _, err := io.ReadFull(br, echo); err != nil || string(echo) != "ping" {
		t.Fatalf("echo = %q, %v", echo, err)
	}

	// PHP tools refuse an app.
	if env := h.covo("php", "check", "front"); env.OK || env.Error == nil || env.Error.Code != "invalid_input" {
		t.Errorf("php check on an app = %+v", env.Error)
	}
	h.mustCovo("site", "remove", "front")
}

type appState struct {
	State    string   `json:"state"`
	Port     int      `json:"port"`
	PID      int      `json:"pid"`
	ExitCode *int     `json:"exit_code"`
	Message  string   `json:"message"`
	LogTail  []string `json:"log_tail"`
}

// appState reads a site's app state from covo site list.
func (h *harness) appState(site string) appState {
	h.t.Helper()
	var sites []struct {
		Name string    `json:"name"`
		App  *appState `json:"app"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil {
		h.t.Fatal(err)
	}
	for _, s := range sites {
		if s.Name == site && s.App != nil {
			return *s.App
		}
	}
	h.t.Fatalf("no app state for %s", site)
	return appState{}
}

// waitApp waits until the app reaches state.
func (h *harness) waitApp(site, state string) appState {
	h.t.Helper()
	var st appState
	for range 120 {
		if st = h.appState(site); st.State == state {
			return st
		}
		time.Sleep(500 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(h.home(), "logs", "sites", site, "app.log"))
	h.t.Fatalf("%s never became %s: %+v\napp.log:\n%s", site, state, st, b)
	return st
}

// readPids waits for the dev server's pid file (server, child, grandchild)
// to be written anew.
func readPids(t *testing.T, path string, old []int) []int {
	t.Helper()
	for range 100 {
		b, err := os.ReadFile(path)
		if err == nil {
			var pids []int
			for _, f := range strings.Fields(string(b)) {
				n, _ := strconv.Atoi(f)
				pids = append(pids, n)
			}
			if len(pids) == 3 && (len(old) == 0 || pids[0] != old[0]) {
				return pids
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("no new pids in %s", path)
	return nil
}

func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func checkAllGone(t *testing.T, when string, pids []int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for _, p := range pids {
		for alive(p) && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if alive(p) {
			t.Errorf("%s: pid %d still running", when, p)
		}
	}
}

// TestAppRun lets Covo start a dev server: through the login shell, on the
// port Covo gave it, restarted on request, never restarted after a crash,
// and stopped with every process it started.
func TestAppRun(t *testing.T) {
	h := newHarness(t)
	tmp := t.TempDir()
	server := filepath.Join(tmp, "devserver")
	if out, err := exec.Command("go", "build", "-o", server, "./testdata/devserver").CombinedOutput(); err != nil {
		t.Fatalf("build devserver: %v\n%s", err, out)
	}
	project := filepath.Join(tmp, "my app")
	if err := os.Mkdir(project, 0o755); err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(tmp, "pids")
	t.Cleanup(func() {
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(h.home(), "logs", "sites", "svc", "app.log"))
			t.Logf("app.log:\n%s", b)
		}
	})

	h.mustCovo("start")
	var added struct {
		AppMode string `json:"app_mode"`
		AppPort int    `json:"app_port"`
		URL     string `json:"url"`
	}
	cmd := fmt.Sprintf("'%s' --pids '%s' --port {port}", server, pidFile)
	if err := json.Unmarshal(h.mustCovo("site", "add", "svc", "--path", project, "--command", cmd), &added); err != nil {
		t.Fatal(err)
	}
	if added.AppMode != "run" || added.AppPort < 41000 {
		t.Fatalf("added = %+v", added)
	}
	h.waitApp("svc", "running")
	pids := readPids(t, pidFile, nil)
	st := h.status()
	var seen struct {
		Port, FlagPort, CA, CovoVars string
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(h.get(added.URL+"/", st.CA.CertPath)), &got); err != nil {
		t.Fatal(err)
	}
	seen.Port, seen.FlagPort, seen.CA, seen.CovoVars = got["port"], got["flag_port"], got["node_extra_ca_certs"], got["covo_vars"]
	want := strconv.Itoa(added.AppPort)
	if seen.Port != want || seen.FlagPort != want || seen.CA != st.CA.CertPath || seen.CovoVars != "" {
		t.Errorf("app saw %+v, want port %s and CA %s", seen, want, st.CA.CertPath)
	}
	if len(st.Orphans) != 0 {
		t.Errorf("the app's agent is reported as an orphan: %v", st.Orphans)
	}

	// Restart: every old process goes, a new server comes.
	h.mustCovo("site", "restart", "svc")
	checkAllGone(t, "after restart", pids)
	pids = readPids(t, pidFile, pids)
	h.waitApp("svc", "running")

	// A crash: the state says so, nothing restarts it, nothing is left.
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get("http://127.0.0.1:" + want + "/crash")
	if err == nil {
		resp.Body.Close()
	}
	crashed := h.waitApp("svc", "error")
	if crashed.ExitCode == nil || *crashed.ExitCode != 3 || len(crashed.LogTail) == 0 {
		t.Errorf("after the crash: %+v", crashed)
	}
	checkAllGone(t, "after the crash", pids)
	time.Sleep(7 * time.Second) // longer than launchd's ThrottleInterval
	if st := h.appState("svc"); st.State != "error" {
		t.Errorf("the app was restarted after a crash: %+v", st)
	}

	// covo start brings an exited app back.
	h.mustCovo("start")
	pids = readPids(t, pidFile, pids)
	h.waitApp("svc", "running")

	// A program the login shell cannot find.
	h.mustCovo("site", "add", "broken", "--path", project, "--command", "covo-no-such-program --port {port}")
	broken := h.waitApp("broken", "error")
	if !strings.Contains(broken.Message, "covo-no-such-program was not found") {
		t.Errorf("missing program: %+v", broken)
	}
	h.mustCovo("site", "remove", "broken")

	// Stop: every process ends and the port is free again.
	h.mustCovo("stop")
	checkAllGone(t, "after stop", pids)
	if st := h.appState("svc"); st.State != "stopped" {
		t.Errorf("after stop: %+v", st)
	}
	if l, err := net.Listen("tcp", "127.0.0.1:"+want); err != nil {
		t.Errorf("port %s still taken after stop: %v", want, err)
	} else {
		l.Close()
	}
	var logs struct {
		Lines []string `json:"lines"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "logs", "svc", "--app"), &logs); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(logs.Lines, "\n"), "devserver listening on "+want) {
		t.Errorf("app log: %q", logs.Lines)
	}
}
