//go:build integration

package e2e

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogRotationReopensFPMLog(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("site", "add", "demo", "--php", "8.4", "--path", newSite(t))
	h.mustCovo("start")
	fpmPID := func() int {
		for _, s := range h.status().Services {
			if s.Name == "php-8.4" {
				return s.PID
			}
		}
		t.Fatal("php-8.4 not listed")
		return 0
	}
	pid := fpmPID()

	log := filepath.Join(h.home(), "logs", "php-8.4-fpm.log")
	f, err := os.OpenFile(log, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(bytes.Repeat([]byte("x"), 11<<20)); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	h.mustCovo("start")
	if got := fpmPID(); got != pid {
		t.Fatalf("FPM was restarted (%d -> %d); it should only reopen its log", pid, got)
	}
	if fi, err := os.Stat(log + ".1"); err != nil || fi.Size() < 11<<20 {
		t.Fatalf("rotated log missing or small: %v", err)
	}
	// FPM recreates the log on SIGUSR1 and notes it there.
	deadline := time.Now().Add(5 * time.Second)
	for {
		b, _ := os.ReadFile(log)
		if strings.Contains(strings.ToLower(string(b)), "re-opened") || strings.Contains(strings.ToLower(string(b)), "reopen") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("FPM did not reopen %s; content: %q", log, string(b))
		}
		time.Sleep(100 * time.Millisecond)
	}
}
