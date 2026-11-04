// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/lock"
)

// The lock is a flock on a fresh file descriptor, so taking it again in the
// same process waits for itself. The convention: exported methods that
// change things take it, unexported ones assume the caller holds it. This
// test reads the package's source and fails when code running under the
// lock calls a method that takes it again.
func TestLockedClosuresDoNotLockAgain(t *testing.T) {
	fset := token.NewFileSet()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	calls := map[string]map[string]bool{}
	var closures []*ast.FuncLit
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range af.Decls {
			fd, ok := d.(*ast.FuncDecl)
			if !ok || fd.Recv == nil || fd.Body == nil || !isCoreReceiver(fd) {
				continue
			}
			calls[fd.Name.Name] = coreCallees(fd.Body)
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				if ce, ok := n.(*ast.CallExpr); ok && isCoreCall(ce, "locked") {
					for _, a := range ce.Args {
						if fl, ok := a.(*ast.FuncLit); ok {
							closures = append(closures, fl)
						}
					}
				}
				return true
			})
		}
	}
	// Every method that reaches c.locked, directly or through others.
	locks := map[string]bool{"locked": true}
	for changed := true; changed; {
		changed = false
		for name, callees := range calls {
			if locks[name] {
				continue
			}
			for callee := range callees {
				if locks[callee] {
					locks[name], changed = true, true
					break
				}
			}
		}
	}
	if len(closures) < 10 {
		t.Fatalf("found only %d locked sections: the parser missed them", len(closures))
	}
	for _, fl := range closures {
		for callee := range coreCallees(fl.Body) {
			if locks[callee] {
				t.Errorf("%s: code under the lock calls c.%s, which takes the lock again (call its unexported variant)",
					fset.Position(fl.Pos()), callee)
			}
		}
	}
}

func isCoreReceiver(fd *ast.FuncDecl) bool {
	st, ok := fd.Recv.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	id, ok := st.X.(*ast.Ident)
	return ok && id.Name == "Core"
}

func isCoreCall(ce *ast.CallExpr, name string) bool {
	se, ok := ce.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := se.X.(*ast.Ident)
	return ok && id.Name == "c" && se.Sel.Name == name
}

func coreCallees(n ast.Node) map[string]bool {
	m := map[string]bool{}
	ast.Inspect(n, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpr); ok {
			if se, ok := ce.Fun.(*ast.SelectorExpr); ok {
				if id, ok := se.X.(*ast.Ident); ok && id.Name == "c" {
					m[se.Sel.Name] = true
				}
			}
		}
		return true
	})
	return m
}

// The operations that move a site's files or replace its database wait for
// any other covo command instead of running alongside it.
func TestDestructiveOperationsTakeTheLock(t *testing.T) {
	c := groupsCore(t)
	held, err := lock.Acquire(context.Background(), c.Layout.Lock)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Unlock() }()

	ops := map[string]func(ctx context.Context) error{
		"RestoreSnapshot": func(ctx context.Context) error { _, err := c.RestoreSnapshot(ctx, "shop", "x"); return err },
		"CreateSnapshot": func(ctx context.Context) error {
			_, err := c.CreateSnapshot(ctx, "shop", SnapshotManual, "")
			return err
		},
		"DeleteSnapshot": func(ctx context.Context) error { return c.DeleteSnapshot(ctx, "shop", "x") },
		"Pull": func(ctx context.Context) error {
			_, err := c.Pull(ctx, "shop", "production", SyncOptions{})
			return err
		},
		"Push": func(ctx context.Context) error { _, err := c.Push(ctx, "shop", "staging", SyncOptions{}); return err },
		"ApplyBlueprint": func(ctx context.Context) error {
			_, err := c.ApplyBlueprint(ctx, "shop", "bp", ApplyOptions{})
			return err
		},
		"UndoMigration": func(ctx context.Context) error { _, err := c.UndoMigration(ctx, "shop"); return err },
	}
	for name, op := range ops {
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		err := op(ctx)
		cancel()
		if errcode.Code(err) != errcode.Locked {
			t.Errorf("%s while another command holds the lock = %v, want %s", name, err, errcode.Locked)
		}
	}
}
