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
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const phonePage = `<?php
echo defined('WP_HOME') ? WP_HOME : 'none', '|', $_SERVER['COVO_LOCAL_URL'] ?? 'https://phonesite.localhost', '/x';
`

// TestPhone opens a site on the Mac's private address under a .local name
// for a few seconds, with Covo's CA, the URL rewrite and the CA page.
func TestPhone(t *testing.T) {
	h := newHarness(t)
	dir := newSite(t)
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(phonePage), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("site", "add", "phonesite", "--php", "8.4", "--path", dir)
	if env := h.covo("phone", "phonesite"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("phone while stopped = %+v", env)
	}
	h.mustCovo("start")
	var ph struct {
		URL       string   `json:"url"`
		PlainURL  string   `json:"plain_url"`
		Host      string   `json:"host"`
		CAPageURL string   `json:"ca_page_url"`
		Addresses []string `json:"addresses"`
		Minutes   int      `json:"minutes_left"`
	}
	env := h.covo("phone", "phonesite", "--minutes", "5")
	if !env.OK && env.Error.Code == "unsupported" {
		t.Skip("this Mac is on no private network")
	}
	if err := json.Unmarshal(h.mustCovo("phone"), &ph); err != nil || len(ph.Addresses) == 0 || ph.Minutes < 4 ||
		!strings.HasPrefix(ph.Host, "phonesite-") || !strings.HasSuffix(ph.Host, ".local") {
		t.Fatalf("phone = %+v, %v", ph, err)
	}
	st := h.status()
	pem, err := os.ReadFile(st.CA.CertPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	// Resolve the .local name to the private address ourselves (mDNS answers
	// are not needed to test what Covo serves).
	addr := ph.Addresses[0]
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool},
		DialContext: func(ctx context.Context, network, hostport string) (net.Conn, error) {
			_, port, _ := net.SplitHostPort(hostport)
			return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort(addr, port))
		},
	}}
	get := func(url string) (string, http.Header, error) {
		var last error
		for range 20 {
			resp, err := client.Get(url)
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				return string(b), resp.Header, nil
			}
			last = err
			time.Sleep(250 * time.Millisecond)
		}
		return "", nil, last
	}
	body, _, err := get(ph.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if want := ph.URL + "|" + ph.URL + "/x"; body != want {
		t.Fatalf("phone page = %q, want %q", body, want)
	}
	// Plain HTTP on the Mac's address: nothing to install on the phone.
	if !strings.HasPrefix(ph.PlainURL, "http://"+ph.Addresses[0]+":") {
		t.Fatalf("plain URL = %q", ph.PlainURL)
	}
	body, _, err = get(ph.PlainURL + "/")
	if err != nil {
		t.Fatal(err)
	}
	if want := ph.PlainURL + "|" + ph.PlainURL + "/x"; body != want {
		t.Fatalf("plain phone page = %q, want %q", body, want)
	}
	page, _, err := get(ph.CAPageURL)
	if err != nil || !strings.Contains(page, "Covo") || !strings.Contains(page, "Name Constraints") {
		t.Fatalf("CA page: %v %.200q", err, page)
	}
	_, hdr, err := get(strings.TrimSuffix(ph.CAPageURL, "/") + "/covo-ca.mobileconfig")
	if err != nil || hdr.Get("Content-Type") != "application/x-apple-aspen-config" {
		t.Fatalf("profile: %v %v", err, hdr)
	}
	// The local address is unchanged.
	var sites []struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil {
		t.Fatal(err)
	}
	if local := h.get(sites[0].URL, st.CA.CertPath); local != "none|"+"https://phonesite.localhost/x" {
		t.Fatalf("local page = %q", local)
	}
	if st := h.status(); len(st.Orphans) != 0 {
		t.Fatalf("orphans while sharing: %v", st.Orphans)
	}

	h.mustCovo("phone", "--off")
	client.Timeout = 2 * time.Second
	if _, err := client.Get(ph.URL + "/"); err == nil {
		t.Fatal("still served after --off")
	}
}
