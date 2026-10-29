// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestTestDomains(t *testing.T) {
	h := newHarness(t)
	resolverFile := filepath.Join(h.resolverDir, "test")

	// .test is refused until enabled; other suffixes are always refused.
	site := newSite(t)
	if env := h.covo("site", "add", "demo", "--domain", "demo.covo.test", "--path", site); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("site add .test before enable: %+v", env.Error)
	}
	for domain, code := range map[string]string{
		"demo.covo": "invalid_input", "example.com": "invalid_input", "demo.local": "unsupported", "Demo.test": "invalid_input",
	} {
		if env := h.covo("site", "add", "x", "--domain", domain, "--path", site); env.OK || env.Error.Code != code {
			t.Errorf("--domain %s: %+v, want %s", domain, env.Error, code)
		}
	}

	var enabled struct {
		Enabled  bool `json:"enabled"`
		Changed  bool `json:"changed"`
		DNSPort  int  `json:"dns_port"`
		Resolver struct {
			Path      string `json:"path"`
			Installed bool   `json:"installed"`
		} `json:"resolver"`
	}
	if err := json.Unmarshal(h.mustCovo("domains", "enable", "test"), &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || !enabled.Changed || !enabled.Resolver.Installed || enabled.Resolver.Path != resolverFile {
		t.Fatalf("enable: %+v", enabled)
	}
	body, err := os.ReadFile(resolverFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "\nnameserver 127.0.0.1\nport "+strconv.Itoa(enabled.DNSPort)+"\n") {
		t.Fatalf("resolver file:\n%s", body)
	}
	if err := json.Unmarshal(h.mustCovo("domains", "enable", "test"), &enabled); err != nil || enabled.Changed {
		t.Fatalf("second enable must change nothing: %+v %v", enabled, err)
	}

	h.mustCovo("site", "add", "demo", "--domain", "demo.covo.test", "--php", "8.4", "--path", site)
	if env := h.covo("site", "add", "other", "--domain", "demo.covo.test", "--path", site); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("duplicate domain: %+v", env.Error)
	}
	h.mustCovo("start")

	var status struct {
		Services []struct {
			Name    string `json:"name"`
			Running bool   `json:"running"`
		} `json:"services"`
		Sites []struct {
			URL string `json:"url"`
		} `json:"sites"`
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &status); err != nil {
		t.Fatal(err)
	}
	if i := slices.IndexFunc(status.Services, func(s struct {
		Name    string `json:"name"`
		Running bool   `json:"running"`
	}) bool {
		return s.Name == "dns"
	}); i < 0 || !status.Services[i].Running {
		t.Fatalf("dns service not running: %+v", status.Services)
	}
	wantURL := "https://demo.covo.test:" + strconv.Itoa(h.port)
	if len(status.Sites) != 1 || status.Sites[0].URL != wantURL {
		t.Fatalf("sites = %+v", status.Sites)
	}

	// The DNS server, as macOS would query it.
	dnsAddr := "127.0.0.1:" + strconv.Itoa(enabled.DNSPort)
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, dnsAddr)
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := r.LookupHost(ctx, "demo.covo.test")
	slices.Sort(ips)
	if err != nil || !slices.Equal(ips, []string{"127.0.0.1", "::1"}) {
		t.Fatalf("lookup demo.covo.test = %v, %v", ips, err)
	}
	if ips, err := r.LookupHost(ctx, "example.com"); err == nil {
		t.Fatalf("example.com resolved to %v", ips)
	}
	out, _ := exec.Command("/usr/sbin/lsof", "-nP", "-a", "-i:"+strconv.Itoa(enabled.DNSPort)).Output()
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n")[1:] {
		if !strings.Contains(line, "127.0.0.1:"+strconv.Itoa(enabled.DNSPort)) {
			t.Errorf("DNS server not on loopback only: %s", line)
		}
	}

	// HTTPS on the .test domain, resolved by Covo's DNS, trusting only Covo's CA.
	pem, err := os.ReadFile(status.CA.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext:     (&net.Dialer{Resolver: r}).DialContext,
	}}
	var got string
	for range 30 {
		resp, err := client.Get(wantURL)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if got = string(b); resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.HasPrefix(got, "8.4.") {
		t.Fatalf("GET %s = %q", wantURL, got)
	}

	// Disable refuses while a site uses .test, then removes everything.
	if env := h.covo("domains", "disable", "test"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("disable with a .test site: %+v", env.Error)
	}
	h.mustCovo("site", "remove", "demo")
	h.mustCovo("domains", "disable", "test")
	if _, err := os.Stat(h.resolverDir); !os.IsNotExist(err) {
		t.Fatalf("resolver dir created by Covo must be gone: %v", err)
	}
	if err := json.Unmarshal(h.mustCovo("status"), &status); err != nil {
		t.Fatal(err)
	}
	for _, s := range status.Services {
		if s.Name == "dns" {
			t.Fatalf("dns service still listed: %+v", status.Services)
		}
	}
	if c, err := net.DialTimeout("tcp", dnsAddr, time.Second); err == nil {
		c.Close()
		t.Fatal("DNS server still listening after disable")
	}
}

func TestUninstallRemovesResolverFile(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("domains", "enable", "test")
	h.mustCovo("start")
	var plan struct {
		Resolver []string `json:"resolver"`
		Done     bool     `json:"done"`
	}
	if err := json.Unmarshal(h.mustCovo("uninstall", "--yes"), &plan); err != nil {
		t.Fatal(err)
	}
	if !plan.Done || len(plan.Resolver) != 1 {
		t.Fatalf("uninstall: %+v", plan)
	}
	if _, err := os.Stat(h.resolverDir); !os.IsNotExist(err) {
		t.Fatalf("resolver dir left after uninstall: %v", err)
	}
}

// A resolver file left by another tool is never overwritten or removed.
func TestForeignResolverFileIsKept(t *testing.T) {
	h := newHarness(t)
	if err := os.MkdirAll(h.resolverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	foreign := "nameserver 127.0.0.1\nport 53\n"
	if err := os.WriteFile(filepath.Join(h.resolverDir, "test"), []byte(foreign), 0o644); err != nil {
		t.Fatal(err)
	}
	if env := h.covo("domains", "enable", "test"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("enable over a foreign file: %+v", env.Error)
	}
	h.mustCovo("uninstall", "--yes")
	if b, _ := os.ReadFile(filepath.Join(h.resolverDir, "test")); string(b) != foreign {
		t.Fatal("foreign resolver file modified")
	}
}
