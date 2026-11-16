// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build !benbowtest

package core

// restoreStage marks a step of a restore; only the end-to-end tests' builds
// can stop there (restorehook_on.go).
func restoreStage(string) {}
