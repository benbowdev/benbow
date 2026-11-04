// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build !covotest

package core

// allowManifestTestKey is false in every build but the end-to-end tests'
// (-tags covotest): a release never trusts a key from the environment.
const allowManifestTestKey = false
