// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package resolver

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
)

func TestRenderParse(t *testing.T) {
	f := File{Home: "/Users/x/Library/Application Support/benbow", Port: 53535, CreatedDir: true}
	got, ok := Parse(f.Render())
	if !ok || got != f {
		t.Fatalf("Parse(Render()) = %+v, %v", got, ok)
	}
	if !strings.Contains(f.Render(), "\nnameserver 127.0.0.1\nport 53535\n") {
		t.Fatalf("missing resolver directives:\n%s", f.Render())
	}
	for _, foreign := range []string{"", "nameserver 127.0.0.1\nport 53\n", "# something else\n" + homeKey + "/x\nport 1\n"} {
		if _, ok := Parse(foreign); ok {
			t.Errorf("Parse accepted a foreign file %q", foreign)
		}
	}
}

// recorder runs commands as the user and records them.
type recorder struct{ cmds []string }

func (r *recorder) run(ctx context.Context, prompt, cmd string) error {
	r.cmds = append(r.cmds, cmd)
	return Direct(ctx, prompt, cmd)
}

func newManager(t *testing.T, dir string) (Manager, *recorder) {
	t.Helper()
	r := &recorder{}
	base := filepath.Join(t.TempDir(), "with space")
	return Manager{Dir: filepath.Join(base, dir), Home: "/benbow home", Run: r.run, TmpDir: filepath.Join(base, "tmp")}, r
}

func TestInstallRemoveCreatedDir(t *testing.T) {
	m, r := newManager(t, "resolver")
	ctx := context.Background()

	changed, err := m.Install(ctx, "test", 53535)
	if err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	st, f, err := m.State("test")
	if err != nil || !st.Installed || st.Port != 53535 || !f.CreatedDir {
		t.Fatalf("state = %+v %+v %v", st, f, err)
	}
	info, _ := os.Stat(m.Path("test"))
	if info.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v, want 0644", info.Mode().Perm())
	}

	// Idempotent: no command, hence no dialog.
	n := len(r.cmds)
	if changed, err := m.Install(ctx, "test", 53535); err != nil || changed || len(r.cmds) != n {
		t.Fatalf("second install: changed=%v err=%v cmds=%d", changed, err, len(r.cmds)-n)
	}
	// Port change rewrites the file and keeps CreatedDir.
	if changed, err := m.Install(ctx, "test", 53600); err != nil || !changed {
		t.Fatalf("port change: changed=%v err=%v", changed, err)
	}
	if _, f, _ := m.State("test"); f.Port != 53600 || !f.CreatedDir {
		t.Fatalf("after port change: %+v", f)
	}

	if changed, err := m.Remove(ctx, "test"); err != nil || !changed {
		t.Fatalf("remove: changed=%v err=%v", changed, err)
	}
	if _, err := os.Stat(m.Dir); !os.IsNotExist(err) {
		t.Fatalf("resolver dir created by Benbow must be removed: %v", err)
	}
	n = len(r.cmds)
	if changed, err := m.Remove(ctx, "test"); err != nil || changed || len(r.cmds) != n {
		t.Fatalf("second remove: changed=%v err=%v", changed, err)
	}
	if entries, _ := os.ReadDir(m.TmpDir); len(entries) != 0 {
		t.Fatalf("staged files left: %v", entries)
	}
}

func TestRemoveKeepsExistingDir(t *testing.T) {
	m, _ := newManager(t, "resolver")
	if err := os.MkdirAll(m.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(m.Dir, "dev")
	if err := os.WriteFile(other, []byte("nameserver 127.0.0.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := m.Install(ctx, "test", 53535); err != nil {
		t.Fatal(err)
	}
	if _, f, _ := m.State("test"); f.CreatedDir {
		t.Fatal("the directory already existed")
	}
	if _, err := m.Remove(ctx, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("another tool's resolver file was touched: %v", err)
	}
}

func TestForeignFileIsNeverTouched(t *testing.T) {
	ctx := context.Background()
	for name, content := range map[string]string{
		"other tool": "nameserver 127.0.0.1\nport 53\n",
		"other home": File{Home: "/another/benbow", Port: 1234}.Render(),
	} {
		t.Run(name, func(t *testing.T) {
			m, r := newManager(t, "resolver")
			if err := os.MkdirAll(m.Dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(m.Path("test"), []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := m.Install(ctx, "test", 53535); errcode.Code(err) != errcode.Conflict {
				t.Fatalf("install over a foreign file: %v", err)
			}
			if changed, err := m.Remove(ctx, "test"); err != nil || changed {
				t.Fatalf("remove of a foreign file: changed=%v err=%v", changed, err)
			}
			if b, _ := os.ReadFile(m.Path("test")); string(b) != content {
				t.Fatal("foreign file modified")
			}
			if len(r.cmds) != 0 {
				t.Fatalf("ran %v", r.cmds)
			}
		})
	}
}

func TestRejectsBadInput(t *testing.T) {
	m, r := newManager(t, "resolver")
	ctx := context.Background()
	for _, zone := range []string{"", "../etc", "a b", "te'st", "x;rm"} {
		if _, err := m.Install(ctx, zone, 53535); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("zone %q: %v", zone, err)
		}
	}
	m.Dir = filepath.Join(t.TempDir(), "it's")
	if _, err := m.Install(ctx, "test", 53535); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("quote in dir: %v", err)
	}
	if len(r.cmds) != 0 {
		t.Fatalf("ran %v", r.cmds)
	}
}

func TestAdminRefusesQuotes(t *testing.T) {
	if err := Admin(context.Background(), "x", `echo "hi"`); errcode.Code(err) != errcode.Internal {
		t.Fatalf("err = %v", err)
	}
}
