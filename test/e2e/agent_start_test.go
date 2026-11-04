// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAgentStart starts a (fake) agent on a site the way "Work with an
// agent" does: session first, temporary permission, Covo connected for the
// run only, the agent in the site's folder; then keep.
func TestAgentStart(t *testing.T) {
	h := newHarness(t)
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	_ = os.MkdirAll(bin, 0o755)
	calls := filepath.Join(home, "calls.log")
	agent := "#!/bin/sh\necho \"$(basename \"$0\") pwd=$(pwd) $*\" >> '" + calls + "'\n"
	for _, n := range []string{"claude", "gemini"} {
		if err := os.WriteFile(filepath.Join(bin, n), []byte(agent), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The terminal: runs the script it is given (the last argument).
	open := filepath.Join(home, "open")
	_ = os.WriteFile(open, []byte("#!/bin/sh\necho \"open $*\" >> '"+calls+"'\nfor a; do last=$a; done\n/bin/sh \"$last\"\n"), 0o755)
	h.env = append(h.env, "COVO_AGENTS_HOME="+home, "COVO_AGENTS_PATH="+bin, "COVO_AGENTS_OPEN="+open)

	dir := newSite(t)
	h.mustCovo("site", "add", "shop", "--path", dir, "--php", "8.4")

	var tools struct {
		Agents []struct {
			ID        string `json:"id"`
			Installed bool   `json:"installed"`
		} `json:"agents"`
	}
	_ = json.Unmarshal(h.mustCovo("agent", "tools"), &tools)
	installed := 0
	for _, a := range tools.Agents {
		if a.Installed {
			installed++
		}
	}
	if installed != 2 {
		t.Fatalf("tools = %+v", tools)
	}

	// Claude Code: Covo for this run only; nothing written in the folder.
	var started struct {
		Session struct {
			Site       string `json:"site"`
			Source     string `json:"source"`
			WriteGrant bool   `json:"write_grant"`
		} `json:"session"`
		Started struct {
			Terminal string `json:"terminal"`
			Script   string `json:"script"`
		} `json:"started"`
	}
	if err := json.Unmarshal(h.mustCovo("agent", "start", "shop", "--tool", "claude"), &started); err != nil {
		t.Fatal(err)
	}
	if started.Session.Source != "launch" || !started.Session.WriteGrant || started.Started.Terminal != "Terminal" {
		t.Fatalf("start = %+v", started)
	}
	log, _ := os.ReadFile(calls)
	resolved, _ := filepath.EvalSymlinks(dir)
	inSite := strings.Contains(string(log), "claude pwd="+resolved+" --mcp-config") || strings.Contains(string(log), "claude pwd="+dir+" --mcp-config")
	if !inSite || !strings.Contains(string(log), "--append-system-prompt") {
		t.Fatalf("calls = %s", log)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "index.php" {
			t.Fatalf("Covo wrote %s into the project", e.Name())
		}
	}
	var perms struct {
		Temporary []string `json:"temporary"`
	}
	_ = json.Unmarshal(h.mustCovo("mcp", "status"), &perms)
	if len(perms.Temporary) != 1 || perms.Temporary[0] != "shop" {
		t.Fatalf("temporary permission = %+v", perms)
	}
	// The script told Covo the agent ended (the fake one exits at once).
	var list []struct {
		AgentRunning bool   `json:"agent_running"`
		LaunchedTool string `json:"launched_tool"`
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		_ = json.Unmarshal(h.mustCovo("agent", "session", "list"), &list)
		if len(list) == 1 && !list[0].AgentRunning && list[0].LaunchedTool == "claude" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions = %+v", list)
		}
	}
	h.mustCovo("agent", "session", "keep", "shop")
	_ = json.Unmarshal(h.mustCovo("mcp", "status"), &perms)
	if len(perms.Temporary) != 0 {
		t.Fatalf("permission left after the session: %+v", perms)
	}
	if _, err := os.Stat(started.Started.Script); !os.IsNotExist(err) {
		t.Fatal("the start script outlived the session")
	}

	// Gemini only sees Covo through its own settings: asked first, then
	// added with a backup.
	settings := filepath.Join(home, ".gemini", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o755)
	_ = os.WriteFile(settings, []byte(`{"theme":"dark"}`), 0o600)
	if env := h.covo("agent", "start", "shop", "--tool", "gemini"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("gemini without --connect = %+v", env)
	}
	h.mustCovo("agent", "start", "shop", "--tool", "gemini", "--connect")
	if b, _ := os.ReadFile(settings); !strings.Contains(string(b), `"covo"`) || !strings.Contains(string(b), `"theme": "dark"`) {
		t.Fatalf("gemini settings = %s", b)
	}
	if b, _ := os.ReadDir(filepath.Join(h.home(), "data", "backups", "agents")); len(b) != 1 {
		t.Fatalf("backups = %v", b)
	}
	h.mustCovo("agent", "session", "keep", "shop")
}
