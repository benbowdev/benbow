// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"testing"

	"github.com/benbowdev/benbow/internal/launchd"
)

func TestNames(t *testing.T) {
	c := &Core{launchctl: &launchd.Launchctl{Prefix: "dev.benbow"}}
	tests := []struct {
		service, label string
	}{
		{caddyService, "dev.benbow.caddy"},
		{phpService("8.4"), "dev.benbow.php84"},
		{phpService("8.2"), "dev.benbow.php82"},
	}
	for _, tt := range tests {
		if got := c.labelFor(tt.service); got != tt.label {
			t.Errorf("labelFor(%q) = %q, want %q", tt.service, got, tt.label)
		}
	}
	if got := socketName("8.4"); got != "php84.sock" {
		t.Errorf("socketName = %q", got)
	}
}
