// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package version

import (
	"net/http"
	"runtime"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

var macOS = sync.OnceValue(func() string {
	v, err := unix.Sysctl("kern.osproductversion")
	if err != nil || v == "" {
		return "unknown"
	}
	return strings.TrimSpace(v)
})

// UserAgent is what Benbow sends on every request it makes to the network:
// "Benbow/0.1.0 (macOS 26.2; arm64)". Nothing that identifies the Mac or
// the user.
func UserAgent() string {
	return userAgent(Version, macOS(), runtime.GOARCH)
}

func userAgent(version, macos, arch string) string {
	return "Benbow/" + version + " (macOS " + macos + "; " + arch + ")"
}

// Transport sets UserAgent on requests that have none, then sends them with
// Base (http.DefaultTransport when nil).
type Transport struct {
	Base http.RoundTripper
}

// RoundTrip implements http.RoundTripper.
func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if req.Header.Get("User-Agent") == "" {
		req = req.Clone(req.Context())
		req.Header.Set("User-Agent", UserAgent())
	}
	return base.RoundTrip(req) //nolint:wrapcheck // a transport passes errors through unchanged
}
