package core

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func TestCheckSiteDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	covo := filepath.Join(home, "Library", "Application Support", "covo")
	sites := filepath.Join(home, "Sites")
	mustMkdir(t, filepath.Join(sites, "demo"), covo, filepath.Join(home, "Library", "Logs"))
	link := filepath.Join(t.TempDir(), "link-to-home")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(sites, "file.txt")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want string // error code, "" for ok
	}{
		{"site under ~/Sites", filepath.Join(sites, "demo"), ""},
		{"~/Sites itself", sites, ""},
		{"disk root", "/", errcode.ForbiddenFolder},
		{"home", home, errcode.ForbiddenFolder},
		{"symlink to home", link, errcode.ForbiddenFolder},
		{"Users", "/Users", errcode.ForbiddenFolder},
		{"System subtree", "/System/Library", errcode.ForbiddenFolder},
		{"usr", "/usr/local", errcode.ForbiddenFolder},
		{"etc via symlink", "/etc", errcode.ForbiddenFolder},
		{"Applications", "/Applications", errcode.ForbiddenFolder},
		{"tmp root", "/tmp", errcode.ForbiddenFolder},
		{"user Library", filepath.Join(home, "Library", "Logs"), errcode.ForbiddenFolder},
		{"Covo home", covo, errcode.ForbiddenFolder},
		{"temp dir is fine", t.TempDir(), ""},
		{"missing", filepath.Join(sites, "nope"), errcode.NotFound},
		{"file", file, errcode.InvalidInput},
		{"relative", "Sites/demo", errcode.InvalidInput},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkSiteDir(tt.path, home, covo)
			if tt.want == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if errcode.Code(err) != tt.want {
				t.Fatalf("err = %v, want code %s", err, tt.want)
			}
		})
	}
}

func TestCheckSiteDirContainingCovoHome(t *testing.T) {
	base := t.TempDir()
	covo := filepath.Join(base, "work", "covo")
	mustMkdir(t, covo)
	if err := checkSiteDir(filepath.Join(base, "work"), filepath.Join(base, "home"), covo); errcode.Code(err) != errcode.ForbiddenFolder {
		t.Fatalf("parent of the Covo home: err = %v", err)
	}
}

func TestHasIndex(t *testing.T) {
	tests := []struct {
		files []string
		want  bool
	}{
		{nil, false},
		{[]string{"readme.txt"}, false},
		{[]string{"index.php"}, true},
		{[]string{"index.html"}, true},
	}
	for _, tt := range tests {
		dir := t.TempDir()
		for _, f := range tt.files {
			if err := os.WriteFile(filepath.Join(dir, f), nil, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if got := hasIndex(dir); got != tt.want {
			t.Errorf("hasIndex(%v) = %v, want %v", tt.files, got, tt.want)
		}
	}
}

func mustMkdir(t *testing.T, dirs ...string) {
	t.Helper()
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
