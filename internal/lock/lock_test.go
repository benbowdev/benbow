// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package lock

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

func TestAcquire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "covo.lock")
	first, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := Acquire(ctx, path); errcode.Code(err) != errcode.Locked {
		t.Fatalf("second Acquire err = %v, want code %q", err, errcode.Locked)
	}

	if err := first.Unlock(); err != nil {
		t.Fatal(err)
	}
	second, err := Acquire(context.Background(), path)
	if err != nil {
		t.Fatalf("Acquire after Unlock: %v", err)
	}
	_ = second.Unlock()
}
