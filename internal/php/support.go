// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package php

import "time"

// Support is how long php.net maintains a PHP version
// (https://www.php.net/supported-versions.php).
type Support struct {
	Minor string `json:"minor"`
	// Status is "active" (bug and security fixes), "security" (security
	// fixes only) or "eol" (no fixes at all any more).
	Status        string `json:"status"`
	ActiveUntil   string `json:"active_until"`   // YYYY-MM-DD
	SecurityUntil string `json:"security_until"` // YYYY-MM-DD
}

// Support statuses.
const (
	SupportActive   = "active"
	SupportSecurity = "security"
	SupportEOL      = "eol"
)

// supportDates are php.net's end of active support and of security fixes.
var supportDates = map[string][2]string{
	"8.0": {"2022-11-26", "2023-11-26"},
	"8.1": {"2023-11-25", "2025-12-31"},
	"8.2": {"2024-12-31", "2026-12-31"},
	"8.3": {"2025-12-31", "2027-12-31"},
	"8.4": {"2026-12-31", "2028-12-31"},
	"8.5": {"2027-12-31", "2029-12-31"},
}

// SupportOf returns the support status of minor at now. Unknown versions
// (newer than this build of Covo) count as active.
func SupportOf(minor string, now time.Time) Support {
	d, ok := supportDates[minor]
	if !ok {
		return Support{Minor: minor, Status: SupportActive}
	}
	s := Support{Minor: minor, ActiveUntil: d[0], SecurityUntil: d[1], Status: SupportActive}
	day := now.UTC().Format(time.DateOnly)
	switch {
	case day > d[1]:
		s.Status = SupportEOL
	case day > d[0]:
		s.Status = SupportSecurity
	}
	return s
}
