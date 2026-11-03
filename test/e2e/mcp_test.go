// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCP drives `covo mcp` over stdio like an agent would.
func TestMCP(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.Command(h.bin, "mcp")
	cmd.Env = h.env
	session, err := mcp.NewClient(&mcp.Implementation{Name: "e2e", Version: "0"}, nil).
		Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

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

	call("add_site", map[string]any{"name": "agent", "path": newSite(t), "php": "8.4"})
	call("start", nil)
	var sites []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	}
	if err := json.Unmarshal([]byte(call("list_sites", nil)), &sites); err != nil || len(sites) != 1 || sites[0].Name != "agent" {
		t.Fatalf("list_sites = %+v, %v", sites, err)
	}
	st := h.status()
	if body := h.get(sites[0].URL, st.CA.CertPath); !strings.HasPrefix(body, "8.4.") {
		t.Fatalf("site added over MCP served %q", body)
	}
	call("update_site", map[string]any{"name": "agent", "enabled": false})
	if !strings.Contains(call("covo_status", nil), `"enabled": false`) {
		t.Fatal("update_site did not turn the site off")
	}
	call("site_logs", map[string]any{"site": "agent", "kind": "access"})
	if !strings.Contains(call("list_php_versions", nil), `"support"`) {
		t.Fatal("list_php_versions lacks support dates")
	}

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "update_site", Arguments: map[string]any{"name": "nope", "php": "8.2"}})
	if err != nil || !res.IsError || !strings.HasPrefix(res.Content[0].(*mcp.TextContent).Text, "not_found:") {
		t.Fatalf("update a missing site = %+v, %v", res, err)
	}
	call("stop", nil)
}
