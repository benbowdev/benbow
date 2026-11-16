// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build benbowtest

package core

import "os"

// EnvRestoreCrash makes a restore exit at the stage it names ("files":
// files restored, database not yet; "done": both restored, old folder not
// yet deleted), so the end-to-end tests can check the recovery. Only in
// -tags benbowtest builds.
const EnvRestoreCrash = "BENBOW_TEST_RESTORE_CRASH"

func restoreStage(stage string) {
	if os.Getenv(EnvRestoreCrash) == stage {
		os.Exit(86)
	}
}
