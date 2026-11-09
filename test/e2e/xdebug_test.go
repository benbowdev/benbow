// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"io"
	"net"
	"runtime"
	"strings"
	"testing"
	"time"
)

const xdebugPage = `<?php
echo extension_loaded('xdebug') ? phpversion('xdebug') . '|' . ini_get('xdebug.mode') . '|' . ini_get('xdebug.start_with_request') : 'off';`

func TestXdebugToggle(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("xdebug.so is built for Apple Silicon only")
	}
	h := newHarness(t)
	url, dir := startedSite(t, h, xdebugPage)
	if got := h.get(url, dir.RootCert()); got != "off" {
		t.Fatalf("before: %q", got)
	}

	var res struct {
		PHPVersion string `json:"php_version"`
		Enabled    bool   `json:"enabled"`
		Available  bool   `json:"available"`
		Version    string `json:"version"`
		Path       string `json:"path"`
		Applied    bool   `json:"applied"`
	}
	if err := json.Unmarshal(h.mustBenbow("php", "xdebug", "on", "8.4"), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || !res.Available || !res.Applied || res.Version != "3.5.3" || !strings.HasSuffix(res.Path, "/xdebug/3.5.3/"+res.PHPVersion+"/xdebug.so") {
		t.Fatalf("on = %+v", res)
	}
	if got := h.get(url, dir.RootCert()); got != "3.5.3|debug|trigger" {
		t.Fatalf("with xdebug: %q", got)
	}

	h.mustBenbow("php", "xdebug", "off", "8.4")
	if got := h.get(url, dir.RootCert()); got != "off" {
		t.Fatalf("after off: %q", got)
	}

	// While stopped the setting is kept for the next start.
	h.mustBenbow("stop")
	if err := json.Unmarshal(h.mustBenbow("php", "xdebug", "on", "8.4"), &res); err != nil {
		t.Fatal(err)
	}
	if res.Applied || !res.Enabled {
		t.Fatalf("on while stopped = %+v", res)
	}
	h.mustBenbow("start")
	if got := h.get(url, dir.RootCert()); got != "3.5.3|debug|trigger" {
		t.Fatalf("after start: %q", got)
	}

	// A step-debugging session reaches an IDE on 127.0.0.1:9003 when
	// triggered (skipped if a real IDE holds the port).
	if ide, err := net.Listen("tcp", "127.0.0.1:9003"); err != nil {
		t.Logf("port 9003 busy, skipping the DBGp check: %v", err)
	} else {
		defer ide.Close()
		got := make(chan string, 1)
		go func() {
			conn, err := ide.Accept()
			if err != nil {
				got <- err.Error()
				return
			}
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
			buf := make([]byte, 4096)
			n, _ := io.ReadAtLeast(conn, buf, 64)
			got <- string(buf[:n])
			// Detach so the request completes.
			_, _ = conn.Write([]byte("detach -i 1\x00"))
		}()
		h.get(url+"/?XDEBUG_TRIGGER=1", dir.RootCert())
		select {
		case msg := <-got:
			if !strings.Contains(msg, "<init ") || !strings.Contains(msg, "xdebug:language_version=\"8.4.") {
				t.Fatalf("DBGp init = %q", msg)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("no DBGp connection from Xdebug")
		}
	}

	// No build for a PHP version Benbow does not pin xdebug for.
	if env := h.benbow("php", "xdebug", "on", "8.1"); env.OK {
		t.Fatal("xdebug on 8.1 succeeded")
	}
}
