// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package agents

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeEnv is a home with some agents: claude and gemini on PATH, Codex
// inside the ChatGPT app, Cursor's app, a broken cursor link.
func fakeEnv(t *testing.T) (Env, string) {
	t.Helper()
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	log := filepath.Join(home, "calls.log")
	script := "#!/bin/sh\necho \"$0 $*\" >> '" + log + "'\n"
	for _, p := range []string{
		filepath.Join(bin, "claude"), filepath.Join(bin, "gemini"),
		filepath.Join(home, "Applications/ChatGPT.app/Contents/Resources/codex-cli/bin/codex"),
	} {
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.MkdirAll(filepath.Join(home, "Applications/Cursor.app"), 0o755)
	_ = os.Symlink(filepath.Join(home, "missing"), filepath.Join(bin, "cursor-agent"))
	return Env{Home: home, Path: bin, Apps: []string{filepath.Join(home, "Applications")}}, log
}

func TestDetect(t *testing.T) {
	env, _ := fakeEnv(t)
	got := map[string]Agent{}
	for _, a := range Detect(context.Background(), env) {
		got[a.ID] = a
	}
	for id, want := range map[string]bool{Claude: true, Gemini: true, Codex: true, Cursor: true, CursorAgent: false, Copilot: false, ClaudeDesktop: false} {
		if got[id].Installed != want {
			t.Errorf("%s installed = %v, want %v (%+v)", id, got[id].Installed, want, got[id])
		}
	}
	if !strings.Contains(got[Codex].Path, "ChatGPT.app") || !got[Claude].PerRun || got[Gemini].PerRun {
		t.Errorf("agents = %+v", got)
	}
}

func TestConnect(t *testing.T) {
	ctx := context.Background()
	env, log := fakeEnv(t)
	backups := filepath.Join(env.Home, "backups")
	settings := filepath.Join(env.Home, ".gemini", "settings.json")
	_ = os.MkdirAll(filepath.Dir(settings), 0o755)
	_ = os.WriteFile(settings, []byte(`{"theme":"dark","mcpServers":{"other":{"command":"x"}}}`), 0o600)

	res, err := Connect(ctx, env, Gemini, "/Apps/Covo.app/bin/covo", backups)
	if err != nil || res.Backup == "" {
		t.Fatalf("connect gemini = %+v, %v", res, err)
	}
	var cfg map[string]any
	b, _ := os.ReadFile(settings)
	_ = json.Unmarshal(b, &cfg)
	servers := cfg["mcpServers"].(map[string]any)
	if cfg["theme"] != "dark" || servers["other"] == nil || servers["covo"].(map[string]any)["command"] != "/Apps/Covo.app/bin/covo" {
		t.Fatalf("settings = %s", b)
	}
	if fi, _ := os.Stat(settings); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode changed to %v", fi.Mode().Perm())
	}
	if orig, _ := os.ReadFile(res.Backup); !strings.Contains(string(orig), `"theme":"dark"`) {
		t.Errorf("backup = %s", orig)
	}
	if again, _ := Connect(ctx, env, Gemini, "/x", backups); !again.Already {
		t.Errorf("second connect = %+v", again)
	}

	// Cursor: a new file. Claude Code and Codex: their own command.
	if _, err := Connect(ctx, env, Cursor, "/covo", backups); err != nil {
		t.Fatal(err)
	}
	if a, _ := Find(ctx, env, Cursor); !a.Connected {
		t.Error("cursor not connected")
	}
	if _, err := Connect(ctx, env, Claude, "/covo", backups); err != nil {
		t.Fatal(err)
	}
	if _, err := Connect(ctx, env, Codex, "/covo", backups); err != nil {
		t.Fatal(err)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "claude mcp add -s user covo -- /covo mcp") || !strings.Contains(string(calls), "codex mcp add covo -- /covo mcp") {
		t.Fatalf("calls = %s", calls)
	}
	if _, err := Connect(ctx, env, Copilot, "/covo", backups); err == nil {
		t.Error("copilot is not installed")
	}

	// A configuration that is not JSON is left alone.
	_ = os.Remove(settings)
	_ = os.WriteFile(settings, []byte("{oops"), 0o600)
	if _, err := Connect(ctx, env, Gemini, "/covo", backups); err == nil {
		t.Error("broken JSON overwritten")
	}
	if b, _ := os.ReadFile(settings); string(b) != "{oops" {
		t.Errorf("broken file changed: %s", b)
	}
}

func TestCommand(t *testing.T) {
	ctx := context.Background()
	env, _ := fakeEnv(t)
	work := filepath.Join(env.Home, "covo-tmp")
	l := Launch{Dir: "/Sites/shop", CovoBin: "/covo", Session: "s1", Context: "Site shop.\nWork in the session.", Prompt: "Call covo_status.", WorkDir: work}
	for _, tt := range []struct {
		id   string
		want []string
	}{
		{Claude, []string{"--mcp-config", "--append-system-prompt", "Call covo_status."}},
		{Codex, []string{"-C", "/Sites/shop", `mcp_servers.covo.command="/covo"`, `mcp_servers.covo.args=["mcp"]`, `developer_instructions="Site shop.\nWork in the session."`}},
		{Gemini, []string{"-i"}},
		{Cursor, []string{"/usr/bin/open", "-a", "/Sites/shop"}},
	} {
		a, _ := Find(ctx, env, tt.id)
		argv, err := Command(a, l)
		if err != nil {
			t.Fatalf("%s: %v", tt.id, err)
		}
		joined := strings.Join(argv, "\x00")
		for _, w := range tt.want {
			if !slices.Contains(argv, w) && !strings.Contains(joined, w) {
				t.Errorf("%s argv %q lacks %q", tt.id, argv, w)
			}
		}
	}
	if b, _ := os.ReadFile(filepath.Join(work, "mcp.json")); !strings.Contains(string(b), `"/covo"`) {
		t.Errorf("mcp.json = %s", b)
	}
	a, _ := Find(ctx, env, Copilot)
	if _, err := Command(a, l); err == nil {
		t.Error("copilot is not installed")
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	env, log := fakeEnv(t)
	t.Setenv("COVO_AGENTS_OPEN", filepath.Join(env.Home, "bin", "claude")) // records its arguments
	a, _ := Find(ctx, env, Gemini)
	work := filepath.Join(env.Home, "covo-tmp")
	st, err := Start(ctx, a, Launch{Dir: "/Sites/it's", CovoBin: "/covo", Session: "s1", Context: "c", Prompt: "p", WorkDir: work, Terminal: "Warp"})
	if err != nil || st.Terminal != "Terminal" {
		t.Fatalf("start = %+v, %v", st, err)
	}
	script, _ := os.ReadFile(st.Script)
	if !strings.Contains(string(script), `cd '/Sites/it'\''s'`) || !strings.Contains(string(script), `agent ended '\''s1'\''`) {
		t.Fatalf("script:\n%s", script)
	}
	if calls, _ := os.ReadFile(log); !strings.Contains(string(calls), "-a Terminal "+st.Script) {
		t.Fatalf("open calls = %s", calls)
	}
}
