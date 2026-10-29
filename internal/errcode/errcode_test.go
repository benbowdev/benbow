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
