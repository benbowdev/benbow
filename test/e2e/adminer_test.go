// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// browser fetches url like a browser would: new cookie jar, redirects.
func browser(t *testing.T, caPath, url string) string {
	t.Helper()
	pem, err := os.ReadFile(caPath)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pem)
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Timeout: 15 * time.Second,
		Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}}}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

// TestAdminerLogin opens Adminer through one-time logins from the daemon.
func TestAdminerLogin(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustBenbow("mysql", "enable")
	h.mustBenbow("site", "add", "shop", "--php", "8.4", "--path", newSite(t))
	h.mustBenbow("start")
	h.mustBenbow("db", "create", "shop")
	sock := h.daemon("start").Socket
	st := h.status()

	var login struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustAPI(sock, "POST", "/v1/adminer/login", `{}`), &login); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(login.URL, "benbow_login=") || strings.Contains(login.URL, "password") {
		t.Fatalf("login URL = %s", login.URL)
	}
	body := browser(t, st.CA.CertPath, login.URL)
	if !strings.Contains(body, "Logout") || !strings.Contains(body, "shop") {
		t.Fatalf("root login did not log in:\n%.500s", body)
	}
	// The same URL again: the token is spent, Adminer shows its login form.
	if body := browser(t, st.CA.CertPath, login.URL); strings.Contains(body, "Logout") {
		t.Fatal("a spent token logged in again")
	}

	if err := json.Unmarshal(h.mustAPI(sock, "POST", "/v1/adminer/login", `{"site":"shop"}`), &login); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(login.URL, "db=shop") {
		t.Fatalf("site login URL = %s", login.URL)
	}
	if body := browser(t, st.CA.CertPath, login.URL); !strings.Contains(body, "Logout") {
		t.Fatalf("site login did not log in:\n%.500s", body)
	}
	if env := h.api(sock, "POST", "/v1/adminer/redeem", `{"token":"`+strings.Repeat("0", 64)+`"}`); env.OK || env.Error.Code != "not_found" {
		t.Fatalf("redeem of an unknown token = %+v", env)
	}
	h.daemon("stop")
}
