// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

type portsData struct {
	HTTPS       int  `json:"https_port"`
	HTTPSListen int  `json:"https_listen_port"`
	HTTP        int  `json:"http_port"`
	HTTPListen  int  `json:"http_listen_port"`
	Forwarded   bool `json:"forwarded"`
	Active      []struct {
		From int `json:"from"`
		To   int `json:"to"`
	} `json:"active"`
}

func (h *harness) ports(args ...string) portsData {
	h.t.Helper()
	var p portsData
	if err := json.Unmarshal(h.mustCovo(append([]string{"ports"}, args...)...), &p); err != nil {
		h.t.Fatal(err)
	}
	return p
}

func (h *harness) pfCalls() string {
	h.t.Helper()
	b, err := os.ReadFile(h.pfLog)
	if err != nil && !os.IsNotExist(err) {
		h.t.Fatal(err)
	}
	_ = os.Remove(h.pfLog)
	return string(b)
}

// TestPorts sets an HTTP redirect port, then the standard ports: addresses
// lose the port, Caddy stays on high ports and the helper forwards 443 and
// 80 only while Covo runs (pf is a stand-in here).
func TestPorts(t *testing.T) {
	h := newHarness(t)
	var site struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", newSite(t)), &site); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("start")
	st := h.status()

	// An HTTP port above 1024: Caddy redirects it itself.
	httpPort := freePort(t)
	p := h.ports("--http", strconv.Itoa(httpPort))
	if p.HTTP != httpPort || p.HTTPListen != httpPort || p.Forwarded {
		t.Fatalf("ports = %+v", p)
	}
	noFollow := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := noFollow.Get("http://demo.localhost:" + strconv.Itoa(httpPort) + "/x?y=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if want := site.URL + "/x?y=1"; resp.StatusCode != 308 || resp.Header.Get("Location") != want {
		t.Fatalf("redirect = %d %q, want 308 %q", resp.StatusCode, resp.Header.Get("Location"), want)
	}

	if env := h.covo("ports", "--https", "80"); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("https 80 = %+v", env)
	}
	if env := h.covo("ports", "--https", strconv.Itoa(httpPort)); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("https = http = %+v", env)
	}

	// 443 and 80: forwarded by the helper to high ports. If another program
	// (MAMP…) has 80, Covo refuses it and the HTTP port stays as it was.
	forwards := 2
	if env := h.covo("ports", "--https", "443", "--http", "80"); !env.OK {
		if env.Error.Code != "port_in_use" || !strings.Contains(env.Error.Message, "port 80") {
			t.Fatalf("standard ports = %+v", env)
		}
		t.Log("port 80 is taken on this Mac: testing 443 only")
		forwards = 1
		p = h.ports("--https", "443")
	} else {
		p = h.ports()
	}
	if p.HTTPS != 443 || p.HTTPSListen < 1024 || !p.Forwarded || len(p.Active) != forwards {
		t.Fatalf("standard ports = %+v", p)
	}
	calls := h.pfCalls()
	wantRules := []string{
		"pfctl -a com.apple/dev.getcovo.hostshelper -f -",
		"rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 443 -> 127.0.0.1 port " + strconv.Itoa(p.HTTPSListen),
		"rdr pass on lo0 inet6 proto tcp from any to ::1 port 443 -> ::1 port " + strconv.Itoa(p.HTTPSListen),
		"pfctl -E",
	}
	if forwards == 2 {
		wantRules = append(wantRules, "rdr pass on lo0 inet6 proto tcp from any to ::1 port 80 -> ::1 port "+strconv.Itoa(p.HTTPListen))
	}
	for _, want := range wantRules {
		if !strings.Contains(calls, want) {
			t.Errorf("pfctl calls lack %q:\n%s", want, calls)
		}
	}
	var list struct {
		HTTPSPort  int    `json:"https_port"`
		AdminerURL string `json:"adminer_url"`
		Sites      []struct {
			URL string `json:"url"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &list); err != nil {
		t.Fatal(err)
	}
	if list.HTTPSPort != 443 || list.Sites[0].URL != "https://demo.localhost" {
		t.Fatalf("status = %+v", list)
	}
	// Caddy answers on the high port for the address without a port.
	if body := h.get("https://demo.localhost:"+strconv.Itoa(p.HTTPSListen), st.CA.CertPath); !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("listen port served %q", body)
	}
	resp, err = noFollow.Get("http://demo.localhost:" + strconv.Itoa(p.HTTPListen) + "/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.Header.Get("Location") != "https://demo.localhost/" {
		t.Fatalf("redirect with 443 = %q", resp.Header.Get("Location"))
	}

	// Stop: 443 and 80 are released; start puts them back.
	h.mustCovo("stop")
	if calls := h.pfCalls(); !strings.Contains(calls, "pfctl -a com.apple/dev.getcovo.hostshelper -F all") || !strings.Contains(calls, "pfctl -X 42") {
		t.Fatalf("stop pfctl calls:\n%s", calls)
	}
	if p := h.ports(); len(p.Active) != 0 {
		t.Fatalf("forwards after stop = %+v", p.Active)
	}
	h.mustCovo("start")
	if p := h.ports(); len(p.Active) != forwards {
		t.Fatalf("forwards after start = %+v", p.Active)
	}

	// Back to a high port: no forwards, and the helper (not needed by
	// .local either) removes itself.
	p = h.ports("--https", strconv.Itoa(h.port), "--http", "0")
	if p.HTTPS != h.port || p.HTTP != 0 || p.Forwarded {
		t.Fatalf("back to high ports = %+v", p)
	}
	for _, f := range h.helperFiles {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("helper file %s still there: %v", f, err)
		}
	}
	if body := h.get("https://demo.localhost:"+strconv.Itoa(h.port), st.CA.CertPath); !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("site after going back served %q", body)
	}
}
