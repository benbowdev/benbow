// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestCode(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"plain error", errors.New("boom"), Internal},
		{"coded", Errorf(PortInUse, "busy"), PortInUse},
		{"wrapped coded", fmt.Errorf("start: %w", New(NotFound, errors.New("x"))), NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Code(tt.err); got != tt.want {
				t.Errorf("Code() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewNil(t *testing.T) {
	if New(Internal, nil) != nil {
		t.Fatal("New(nil) should be nil")
	}
}

// The exit codes are part of the CLI's contract: this table never changes,
// it only grows.
func TestExitCode(t *testing.T) {
	want := map[string]int{
		Internal: 1, Service: 1,
		InvalidInput: 2, ForbiddenFolder: 2,
		NotFound: 3,
		Conflict: 4, PortInUse: 4, Locked: 4,
		ConfirmationRequired: 5,
		Forbidden:            6,
		Unsupported:          7,
		Download:             8, Checksum: 8,
		"some_future_code": 1,
	}
	for code, n := range want {
		if got := ExitCode(code); got != n {
			t.Errorf("ExitCode(%q) = %d, want %d", code, got, n)
		}
	}
}
