// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build benbowtest

package core

import (
	"os"

	"github.com/benbowdev/benbow/internal/components"
)

// allowManifestTestKey lets the end-to-end tests sign manifests with their
// own key (BENBOW_MANIFEST_TEST_KEY).
const allowManifestTestKey = true

// Test manifests may point at the test's own HTTP server on loopback.
func init() {
	components.AllowLoopbackHTTP = os.Getenv(EnvManifestTestKey) != "" && os.Getenv("BENBOW_HOME") != ""
}
