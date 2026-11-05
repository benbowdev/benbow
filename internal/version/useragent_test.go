// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package version

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
)

func TestUserAgent(t *testing.T) {
	if got := userAgent("0.1.0-beta.4", "26.2", "arm64"); got != "Covo/0.1.0-beta.4 (macOS 26.2; arm64)" {
		t.Errorf("userAgent = %q", got)
	}
	if got := UserAgent(); !regexp.MustCompile(`^Covo/\S+ \(macOS ([0-9.]+|unknown); (arm64|amd64)\)$`).MatchString(got) {
		t.Errorf("UserAgent() = %q", got)
	}
}

func TestTransport(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = append(got, r.UserAgent())
	}))
	defer srv.Close()
	client := &http.Client{Transport: Transport{}}
	for _, ua := range []string{"", "custom"} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		if ua != "" {
			req.Header.Set("User-Agent", ua)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	if len(got) != 2 || got[0] != UserAgent() || got[1] != "custom" {
		t.Fatalf("user agents = %q", got)
	}
}
