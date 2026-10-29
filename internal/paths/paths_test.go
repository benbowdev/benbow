package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	long := "/Users/someone/" + strings.Repeat("very-long-directory-name/", 4) + "covo"
	tests := []struct {
		name         string
		root         string
		wantErr      bool
		wantFallback bool
	}{
		{"standard home", "/Users/andrea/Library/Application Support/covo", false, false},
		{"relative path", "covo", true, false},
		{"socket path too long", long, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l, err := New(tt.root)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			inRoot := l.Run == filepath.Join(tt.root, "run")
			if inRoot == tt.wantFallback {
				t.Fatalf("Run = %q, fallback expected: %v", l.Run, tt.wantFallback)
			}
			if got := len(l.Socket(longestSocketName)); got > maxSocketPath {
				t.Fatalf("socket path is %d bytes, max %d", got, maxSocketPath)
			}
		})
	}
}

func TestFallbackIsPerRoot(t *testing.T) {
	if fallbackRunDir("/a") == fallbackRunDir("/b") {
		t.Fatal("different roots must not share a socket directory")
	}
}

func TestEnsure(t *testing.T) {
	l, err := New(filepath.Join(t.TempDir(), "Covo Home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	// Idempotent.
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{l.Data, l.Run, l.Tmp} {
		fi, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o700 {
			t.Errorf("%s mode = %v, want 0700", dir, fi.Mode().Perm())
		}
	}
}

func TestEnsureRejectsOpenRunDir(t *testing.T) {
	l, err := New(filepath.Join(t.TempDir(), "covo"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	// Ensure re-applies the mode, so tamper after it and check directly.
	if err := os.Chmod(l.Run, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := l.checkRunDir(); err == nil {
		t.Fatal("expected world-accessible run dir to be rejected")
	}
}
