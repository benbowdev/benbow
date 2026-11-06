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
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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
