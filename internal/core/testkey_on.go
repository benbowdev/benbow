// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build covotest

package core

// allowManifestTestKey lets the end-to-end tests sign manifests with their
// own key (COVO_MANIFEST_TEST_KEY).
const allowManifestTestKey = true
