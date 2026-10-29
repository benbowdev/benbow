// Package lock serialises state-changing Covo commands with an advisory
// flock on a file, so two CLI invocations never rewrite configs concurrently.
package lock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

const pollInterval = 100 * time.Millisecond

// Lock is a held lock. Release it with Unlock.
type Lock struct {
	f *os.File
}

// Acquire takes an exclusive lock on path, waiting until ctx is done.
func Acquire(ctx context.Context, path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return &Lock{f: f}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = f.Close()
			return nil, fmt.Errorf("lock %s: %w", path, err)
		}
		select {
		case <-ctx.Done():
			_ = f.Close()
			return nil, errcode.Errorf(errcode.Locked, "another covo command is running")
		case <-time.After(pollInterval):
		}
	}
}

// Unlock releases the lock.
func (l *Lock) Unlock() error {
	// Closing the descriptor releases the flock.
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	return nil
}
