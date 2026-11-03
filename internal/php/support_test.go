// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package php

import (
	"testing"
	"time"
)

func TestSupportOf(t *testing.T) {
	day := func(s string) time.Time {
		d, err := time.Parse(time.DateOnly, s)
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	for _, tt := range []struct {
		minor, on, want string
	}{
		{"8.0", "2026-11-03", SupportEOL},
		{"8.1", "2025-12-31", SupportSecurity},
		{"8.1", "2026-01-01", SupportEOL},
		{"8.2", "2026-11-03", SupportSecurity},
		{"8.4", "2026-11-03", SupportActive},
		{"8.4", "2027-01-01", SupportSecurity},
		{"9.0", "2026-11-03", SupportActive},
	} {
		if got := SupportOf(tt.minor, day(tt.on)).Status; got != tt.want {
			t.Errorf("SupportOf(%s, %s) = %s, want %s", tt.minor, tt.on, got, tt.want)
		}
	}
}
