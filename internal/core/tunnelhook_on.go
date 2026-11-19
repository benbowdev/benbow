// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build benbowtest

package core

import (
	"context"
	"os"
)

// EnvTunnelNoDNS makes the public link ready without asking public DNS
// for its name, so the end-to-end tests can use a fake cloudflared. Only in
// -tags benbowtest builds.
const EnvTunnelNoDNS = "BENBOW_TEST_TUNNEL_NO_DNS"

func init() {
	if os.Getenv(EnvTunnelNoDNS) != "" {
		tunnelResolvesFunc = func(context.Context, string) bool { return true }
	}
}
