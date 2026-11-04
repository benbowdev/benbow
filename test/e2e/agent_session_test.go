// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestAgentSession starts `covo mcp` inside a WordPress site's folder, like
// an agent started there: the session opens at connection (database saved
// by a background process), many changes keep the starting point, a risky
// one gets a checkpoint, and undo puts files and database back.
func TestAgentSession(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")
	root := filepath.Join(t.TempDir(), "shop")
	h.mustCovo("wordpress", "shop", "--path", root)
	h.mustCovo("mcp", "allow", "shop")
	h.mustCovo("snapshot", "keep", "3")
	blogname := func() string {
		out, code := h.wp("shop", "option", "get", "blogname")
		if code != 0 {
			t.Fatalf("blogname: %s", out)
		}
		return strings.TrimSpace(out)
	}
	before := blogname()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.Command(h.bin, "mcp")
	cmd.Env, cmd.Dir, cmd.Stderr = h.env, filepath.Join(root, "wp-content"), os.Stderr
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e-agent", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	type info struct {
		Site         string `json:"site"`
		Agent        string `json:"agent"`
		Source       string `json:"source"`
		BaseSnapshot string `json:"base_snapshot"`
		DBSaved      bool   `json:"database_saved"`
		Base         *struct {
			Database string `json:"database"`
		} `json:"base"`
		Checkpoints []struct{ ID string } `json:"checkpoints"`
	}
	var s info
	for deadline := time.Now().Add(2 * time.Minute); ; {
		var list []info
		_ = json.Unmarshal(h.mustCovo("agent", "session", "list"), &list)
		if len(list) == 1 && list[0].DBSaved {
			s = list[0]
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no session with a saved database: %+v", list)
		}
		time.Sleep(500 * time.Millisecond)
	}
	if s.Site != "shop" || s.Source != "mcp-folder" || s.Agent != "e2e-agent" || s.Base == nil || s.Base.Database != "shop" {
		t.Fatalf("session = %+v", s)
	}

	call := func(name string, args map[string]any) string {
		t.Helper()
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError {
			t.Fatalf("%s failed: %s", name, text)
		}
		return text
	}
	// A file changed with the agent's own tools, then many small changes.
	if err := os.WriteFile(filepath.Join(root, "agent-notes.php"), []byte("<?php // by the agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	for i := range 12 {
		out := call("wp", map[string]any{"site": "shop", "args": []string{"option", "update", "blogname", "Agent " + string(rune('A'+i))}})
		if strings.Contains(out, `"checkpoint"`) {
			t.Fatalf("checkpoint for a plain option update: %s", out)
		}
	}
	for range 4 {
		h.mustCovo("snapshot", "create", "shop")
	}
	// A risky one: a plugin from a zip.
	zip := filepath.Join(t.TempDir(), "covo-e2e.zip")
	if err := os.WriteFile(zip, zipOf(t, map[string]string{"covo-e2e/covo-e2e.php": "<?php\n/*\nPlugin Name: Covo E2E\n*/\n"}), 0o644); err != nil {
		t.Fatal(err)
	}
	out := call("wp", map[string]any{"site": "shop", "args": []string{"plugin", "install", zip, "--activate"}})
	if !strings.Contains(out, `"checkpoint"`) {
		t.Fatalf("no checkpoint before a plugin install: %s", out)
	}
	if !strings.Contains(call("session_status", map[string]any{"site": "shop"}), s.BaseSnapshot) {
		t.Fatal("session_status lacks the starting snapshot")
	}
	var snaps []struct {
		ID   string `json:"id"`
		Kind string `json:"kind"`
	}
	_ = json.Unmarshal(h.mustCovo("snapshot", "list", "shop"), &snaps)
	found := false
	for _, sn := range snaps {
		found = found || sn.ID == s.BaseSnapshot
	}
	if !found {
		t.Fatalf("the starting snapshot was pruned: %+v", snaps)
	}

	// Undo everything.
	if env := h.covo("agent", "session", "revert", "shop"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("revert without --yes = %+v", env)
	}
	h.mustCovo("agent", "session", "revert", "shop", "--yes")
	if got := blogname(); got != before {
		t.Fatalf("blogname after undo = %q, want %q", got, before)
	}
	if _, err := os.Stat(filepath.Join(root, "agent-notes.php")); !os.IsNotExist(err) {
		t.Fatal("the agent's own file survived the undo")
	}
	if _, err := os.Stat(filepath.Join(root, "wp-content", "plugins", "covo-e2e")); !os.IsNotExist(err) {
		t.Fatal("the plugin survived the undo")
	}
	var open []info
	_ = json.Unmarshal(h.mustCovo("agent", "session", "list"), &open)
	if len(open) != 0 {
		t.Fatalf("session still open: %+v", open)
	}
}
