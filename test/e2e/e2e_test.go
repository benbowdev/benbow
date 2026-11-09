// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

// Package e2e drives the real benbow binary end to end: it downloads binaries,
// starts launchd agents and serves HTTPS. Run with:
//
//	go test -tags integration ./test/e2e/
//
// Everything is isolated from a real installation: a temporary BENBOW_HOME, a
// test-only launchd label prefix and a free HTTPS port.
package e2e

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

type envelope struct {
	Schema int             `json:"schema"`
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

type harness struct {
	t    *testing.T
	bin  string
	env  []string
	port int
	// resolverDir stands in for /etc/resolver.
	resolverDir string
	// hostsFile stands in for /etc/hosts; hostsOriginal is its initial
	// content (a copy of the real one, MAMP lines included).
	hostsFile, hostsOriginal string
	// helperFiles stand in for the helper's binary and plist.
	helperFiles []string
	// pfLog records what the test helper asked pfctl to do.
	pfLog string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "benbow")
	build := exec.Command("go", "build", "-tags", "benbowtest", "-o", bin, "github.com/benbowdev/benbow/cmd/benbow")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build benbow: %v\n%s", err, out)
	}
	helper := filepath.Join(tmp, "benbow-hostshelper")
	build = exec.Command("go", "build", "-o", helper, "github.com/benbowdev/benbow/cmd/benbow-hostshelper")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build benbow-hostshelper: %v\n%s", err, out)
	}
	// A space in the path on purpose: the real home is "Application Support".
	home := filepath.Join(tmp, "Benbow Home")
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	h := &harness{
		t:    t,
		bin:  bin,
		port: freePort(t),
	}
	h.env = append(os.Environ(),
		"BENBOW_HOME="+home,
		"BENBOW_LABEL_PREFIX=dev.benbow.test-"+hex.EncodeToString(suffix),
		// Never let a test find, or remove, the real "Benbow Local CA".
		"BENBOW_CA_NAME=Benbow Test CA "+hex.EncodeToString(suffix),
		"BENBOW_HTTPS_PORT="+strconv.Itoa(h.port),
		// Never touch /etc/resolver, never ask for an administrator password.
		"BENBOW_RESOLVER_DIR="+filepath.Join(tmp, "resolver"),
	)
	h.resolverDir = filepath.Join(tmp, "resolver")
	h.env = append(h.env, "BENBOW_HOSTS_HELPER_SOCKET="+h.startHelper(helper))
	t.Cleanup(func() {
		h.benbow("stop")
		h.benbow("uninstall", "--yes")
		// Safety net: never leave a test agent loaded, whatever benbow did.
		for _, label := range h.loadedLabels() {
			_ = exec.Command("/bin/launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label).Run()
		}
	})
	return h
}

// startHelper runs the hosts helper as the current user on a copy of the
// real /etc/hosts, and returns its socket. Tests never install the real one.
func (h *harness) startHelper(bin string) string {
	t := h.t
	// Short: Unix socket paths are limited to 104 bytes.
	dir, err := os.MkdirTemp("", "hs")
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile("/etc/hosts")
	if err != nil {
		t.Fatal(err)
	}
	h.hostsOriginal = string(current)
	h.hostsFile = filepath.Join(dir, "hosts")
	if err := os.WriteFile(h.hostsFile, current, 0o644); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "h.sock")
	// Never the real pf: a stand-in records each call and its rules.
	h.pfLog = filepath.Join(dir, "pfctl.log")
	fakePF := filepath.Join(dir, "pfctl")
	script := "#!/bin/sh\n{ echo \"pfctl $*\"; cat; } >> '" + h.pfLog + "'\n[ \"$1\" = -E ] && echo 'Token : 42'\nexit 0\n"
	if err := os.WriteFile(fakePF, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	args := []string{"--uid", strconv.Itoa(os.Getuid()), "--socket", sock, "--hosts", h.hostsFile, "--flush=false", "--pfctl", fakePF}
	for _, name := range []string{"helper-binary", "helper-plist"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
		h.helperFiles = append(h.helperFiles, p)
		args = append(args, "--remove", p)
	}
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		<-done
		_ = os.RemoveAll(dir)
	})
	for range 50 {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return sock
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("helper did not start")
	return ""
}

func (h *harness) hosts() string {
	h.t.Helper()
	b, err := os.ReadFile(h.hostsFile)
	if err != nil {
		h.t.Fatal(err)
	}
	return string(b)
}

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// benbow runs the binary with --json and returns the decoded envelope.
func (h *harness) benbow(args ...string) envelope {
	h.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.bin, append(args, "--json")...)
	cmd.Env = h.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	_ = cmd.Run()
	var env envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		h.t.Fatalf("benbow %s: invalid JSON (%v)\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return env
}

func (h *harness) mustBenbow(args ...string) json.RawMessage {
	h.t.Helper()
	env := h.benbow(args...)
	if !env.OK {
		h.t.Fatalf("benbow %s failed: %+v", strings.Join(args, " "), env.Error)
	}
	return env.Data
}

// get fetches url trusting only Benbow's local CA.
func (h *harness) get(url, caPath string) string {
	h.t.Helper()
	pem, err := os.ReadFile(caPath)
	if err != nil {
		h.t.Fatalf("read CA: %v", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		h.t.Fatal("CA file has no certificates")
	}
	client := &http.Client{
		Timeout:   10 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}},
	}
	var lastErr error
	for range 30 { // FPM and Caddy may need a moment after start
		resp, err := client.Get(url)
		if err == nil {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return string(body)
			}
			lastErr = &statusError{resp.StatusCode, string(body)}
		} else {
			lastErr = err
		}
		time.Sleep(500 * time.Millisecond)
	}
	h.t.Fatalf("GET %s: %v", url, lastErr)
	return ""
}

type statusError struct {
	code int
	body string
}

func (e *statusError) Error() string { return strconv.Itoa(e.code) + ": " + e.body }

func newSite(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "my site")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte("<?php echo PHP_VERSION;"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSiteAddServesHTTPS(t *testing.T) {
	h := newHarness(t)

	var added struct {
		URL string `json:"url"`
	}
	data := h.mustBenbow("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	if err := json.Unmarshal(data, &added); err != nil {
		t.Fatal(err)
	}
	wantURL := "https://demo.localhost:" + strconv.Itoa(h.port)
	if added.URL != wantURL {
		t.Fatalf("url = %q, want %q", added.URL, wantURL)
	}

	h.mustBenbow("start")

	var status struct {
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
	}
	if err := json.Unmarshal(h.mustBenbow("status"), &status); err != nil {
		t.Fatal(err)
	}

	body := h.get(added.URL, status.CA.CertPath)
	if !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("body = %q, want PHP 8.4.x", body)
	}
}
