package core

import (
	"os"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func TestCheckRemovable(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		root string
		ok   bool
	}{
		{home + "/Library/Application Support/covo", true},
		{t.TempDir(), true},
		{"/", false},
		{home, false},
		{"/Users", false},
	}
	for _, tt := range tests {
		err := checkRemovable(tt.root)
		if tt.ok && err != nil {
			t.Errorf("checkRemovable(%q) = %v", tt.root, err)
		}
		if !tt.ok && errcode.Code(err) != errcode.ForbiddenFolder {
			t.Errorf("checkRemovable(%q) = %v, want forbidden_folder", tt.root, err)
		}
	}
}
