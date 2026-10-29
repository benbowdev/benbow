//go:build integration

// Package e2e drives the real covo binary end to end: it downloads binaries,
// starts launchd agents and serves HTTPS. Run with:
//
//	go test -tags integration ./test/e2e/
//
// Everything is isolated from a real installation: a temporary COVO_HOME, a
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
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "covo")
	build := exec.Command("go", "build", "-o", bin, "github.com/getcovo/covo/cmd/covo")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build covo: %v\n%s", err, out)
	}
	// A space in the path on purpose: the real home is "Application Support".
	home := filepath.Join(tmp, "Covo Home")
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	h := &harness{
		t:    t,
		bin:  bin,
		port: freePort(t),
	}
	h.env = append(os.Environ(),
		"COVO_HOME="+home,
		"COVO_LABEL_PREFIX=dev.getcovo.test-"+hex.EncodeToString(suffix),
		"COVO_HTTPS_PORT="+strconv.Itoa(h.port),
	)
	t.Cleanup(func() { h.covo("uninstall", "--yes") })
	return h
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

// covo runs the binary with --json and returns the decoded envelope.
func (h *harness) covo(args ...string) envelope {
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
		h.t.Fatalf("covo %s: invalid JSON (%v)\nstdout: %s\nstderr: %s",
			strings.Join(args, " "), err, stdout.String(), stderr.String())
	}
	return env
}

func (h *harness) mustCovo(args ...string) json.RawMessage {
	h.t.Helper()
	env := h.covo(args...)
	if !env.OK {
		h.t.Fatalf("covo %s failed: %+v", strings.Join(args, " "), env.Error)
	}
	return env.Data
}

// get fetches url trusting only Covo's local CA.
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
	data := h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	if err := json.Unmarshal(data, &added); err != nil {
		t.Fatal(err)
	}
	wantURL := "https://demo.localhost:" + strconv.Itoa(h.port)
	if added.URL != wantURL {
		t.Fatalf("url = %q, want %q", added.URL, wantURL)
	}

	h.mustCovo("start")

	var status struct {
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &status); err != nil {
		t.Fatal(err)
	}

	body := h.get(added.URL, status.CA.CertPath)
	if !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("body = %q, want PHP 8.4.x", body)
	}
}
