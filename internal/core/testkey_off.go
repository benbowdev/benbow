// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build !benbowtest

package core

// allowManifestTestKey is false in every build but the end-to-end tests'
// (-tags benbowtest): a release never trusts a key from the environment.
const allowManifestTestKey = false
