// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package mcpserver

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func connect(t *testing.T, open Open) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	st, ct := mcp.NewInMemoryTransports()
	ss, err := New(open).Connect(ctx, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func TestTools(t *testing.T) {
	opened := 0
	cs := connect(t, func(context.Context) (*core.Core, error) {
		opened++
		return nil, errcode.Errorf(errcode.Service, "no core in this test")
	})
	ctx := context.Background()
	res, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if strings.Contains(tool.Name, "remove") || strings.Contains(tool.Name, "delete") || strings.Contains(tool.Name, "drop") {
			t.Errorf("tool %s deletes: removal stays with the user", tool.Name)
		}
	}
	for _, want := range []string{"covo_status", "list_sites", "site_logs", "list_php_versions", "php_compatibility_check", "wp", "add_site", "new_wordpress",
		"session_status", "session_diff", "fetch_page", "mail_messages", "read_mail", "query_database"} {
		if !slices.Contains(names, want) {
			t.Errorf("no tool %s in %v", want, names)
		}
	}

	// Core errors reach the agent as tool errors with Covo's code.
	r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "list_sites"})
	if err != nil || !r.IsError || !strings.HasPrefix(r.Content[0].(*mcp.TextContent).Text, "service_failed:") {
		t.Fatalf("list_sites = %+v, %v", r, err)
	}

	// Refused before opening the core.
	opened = 0
	for _, args := range []map[string]any{
		{"site": "demo", "args": []string{"user", "update", "admin", "--prompt=user_pass"}},
		{"site": "demo", "args": []string{"shell"}},
		{"site": "demo", "args": []string{}},
	} {
		r, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: "wp", Arguments: args})
		if err != nil || !r.IsError {
			t.Errorf("wp %v = %+v, %v", args, r, err)
		}
	}
	r, err = cs.CallTool(ctx, &mcp.CallToolParams{Name: "add_site", Arguments: map[string]any{"name": "x", "path": "relative/dir"}})
	if err != nil || !r.IsError {
		t.Errorf("add_site with a relative path = %+v, %v", r, err)
	}
	if opened != 0 {
		t.Fatalf("core opened %d times for refused calls", opened)
	}
}
