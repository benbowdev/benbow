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
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	beginMarker = "# BEGIN Covo: managed automatically, do not edit"
	endMarker   = "# END Covo"
)

func TestLocalDomains(t *testing.T) {
	h := newHarness(t)
	site := newSite(t)

	if env := h.covo("site", "add", "shop", "--domain", "shop.local", "--path", site); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("site add .local before enable: %+v", env.Error)
	}

	var enabled struct {
		Enabled bool `json:"enabled"`
		Helper  struct {
			Running bool     `json:"running"`
			Names   []string `json:"names"`
		} `json:"helper"`
	}
	if err := json.Unmarshal(h.mustCovo("domains", "enable", "local"), &enabled); err != nil {
		t.Fatal(err)
	}
	if !enabled.Enabled || !enabled.Helper.Running || len(enabled.Helper.Names) != 0 {
		t.Fatalf("enable local: %+v", enabled)
	}
	if h.hosts() != h.hostsOriginal {
		t.Fatal("enabling without sites changed the hosts file")
	}

	h.mustCovo("site", "add", "shop", "--domain", "shop.local", "--php", "8.4", "--path", site)
	h.mustCovo("site", "add", "blog", "--domain", "blog.covo.local", "--php", "8.4", "--path", site)
	// A .local alias is written as well; a .localhost one is not needed.
	h.mustCovo("site", "set", "shop", "--aliases", "www.shop.local,shop.localhost")
	got := h.hosts()
	original := h.hostsOriginal
	if !strings.HasSuffix(original, "\n") {
		original += "\n"
	}
	wantBlock := beginMarker + "\n" +
		"127.0.0.1\tblog.covo.local\n::1\t\tblog.covo.local\n" +
		"127.0.0.1\tshop.local\n::1\t\tshop.local\n" +
		"127.0.0.1\twww.shop.local\n::1\t\twww.shop.local\n" +
		endMarker + "\n"
	if got != original+wantBlock {
		t.Fatalf("hosts file:\n%s", got)
	}

	// Served over HTTPS (hosts resolution is simulated: the file is a copy).
	h.mustCovo("start")
	var status struct {
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &status); err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(status.CA.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(h.port))
		},
	}}
	url := "https://shop.local:" + strconv.Itoa(h.port)
	var body string
	for range 30 {
		resp, err := client.Get(url)
		if err == nil {
			b, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if body = string(b); resp.StatusCode == http.StatusOK {
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	if !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("GET %s = %q", url, body)
	}

	if env := h.covo("domains", "disable", "local"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("disable with .local sites: %+v", env.Error)
	}
	h.mustCovo("site", "remove", "blog")
	h.mustCovo("site", "remove", "shop")
	if h.hosts() != h.hostsOriginal {
		t.Fatalf("removing the sites did not restore the hosts file:\n%s", h.hosts())
	}

	h.mustCovo("domains", "disable", "local")
	for _, f := range h.helperFiles {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("helper file %s not removed by disable", f)
		}
	}
}

func TestUninstallClearsHostsLines(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("domains", "enable", "local")
	h.mustCovo("site", "add", "shop", "--domain", "shop.local", "--php", "8.4", "--path", newSite(t))
	if !strings.Contains(h.hosts(), "shop.local") {
		t.Fatal("no hosts line")
	}
	h.mustCovo("uninstall", "--yes")
	if h.hosts() != h.hostsOriginal {
		t.Fatalf("uninstall left the hosts file changed:\n%s", h.hosts())
	}
}

// Without a test helper, a test installation refuses to install the system
// helper (and never shows a dialog).
func TestNoSystemHelperFromTests(t *testing.T) {
	h := newHarness(t)
	var env []string
	for _, e := range h.env {
		if !strings.HasPrefix(e, "COVO_HOSTS_HELPER_SOCKET=") {
			env = append(env, e)
		}
	}
	h.env = env
	if _, err := os.Stat("/Library/LaunchDaemons/dev.getcovo.hostshelper.plist"); err == nil {
		t.Skip("a real helper is installed: its socket would answer")
	}
	if res := h.covo("domains", "enable", "local"); res.OK || res.Error.Code != "unsupported" {
		t.Fatalf("enable local with a test prefix: %+v", res.Error)
	}
}
