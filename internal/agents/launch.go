// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Launch is one agent start in a site's folder.
type Launch struct {
	Dir     string // the site's project folder
	CovoBin string // covo, for the MCP server and `covo agent ended`
	Session string // the agent session id
	// Context is the long text for the agent (site, session, recommended
	// workflow); Prompt is the one-line first message.
	Context string
	Prompt  string
	// WorkDir is a folder of Covo's (never the project) for the files the
	// start needs: the context, a one-run MCP configuration, the script.
	WorkDir string
	// Terminal is the terminal app's name (Terminal, iTerm, Ghostty, …).
	Terminal string
}

// Started is how the agent was started.
type Started struct {
	Agent    string   `json:"agent"`
	Terminal string   `json:"terminal,omitempty"`
	Script   string   `json:"script,omitempty"`
	Command  []string `json:"command"`
	// NeedsGlobal is set when the agent only sees Covo through its global
	// configuration (Gemini, Cursor), which is not there.
	NeedsGlobal bool `json:"needs_global,omitempty"`
}

// shellQuote quotes s for sh.
func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// tomlString is a TOML basic string (JSON's escapes are valid TOML).
func tomlString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Command returns the agent's command line for l. Files it needs (the
// context, a one-run MCP configuration) are written to l.WorkDir.
func Command(a Agent, l Launch) ([]string, error) {
	if !a.Installed {
		return nil, fmt.Errorf("%s is not installed on this Mac", a.Name)
	}
	if err := os.MkdirAll(l.WorkDir, 0o700); err != nil {
		return nil, fmt.Errorf("create %s: %w", l.WorkDir, err)
	}
	mcpJSON := func() (string, error) {
		cfg := map[string]any{"mcpServers": map[string]any{ServerName: map[string]any{"command": l.CovoBin, "args": []string{"mcp"}}}}
		b, _ := json.MarshalIndent(cfg, "", "  ")
		p := filepath.Join(l.WorkDir, "mcp.json")
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return "", fmt.Errorf("write %s: %w", p, err)
		}
		return p, nil
	}
	ctxFile := filepath.Join(l.WorkDir, "context.md")
	if err := os.WriteFile(ctxFile, []byte(l.Context), 0o600); err != nil {
		return nil, fmt.Errorf("write %s: %w", ctxFile, err)
	}
	full := l.Context + "\n\n" + l.Prompt
	switch a.ID {
	case Claude:
		p, err := mcpJSON()
		if err != nil {
			return nil, err
		}
		return []string{a.Path, "--mcp-config", p, "--append-system-prompt", l.Context, l.Prompt}, nil
	case Codex:
		return []string{a.Path, "-C", l.Dir,
			"-c", "mcp_servers." + ServerName + ".command=" + tomlString(l.CovoBin),
			"-c", "mcp_servers." + ServerName + `.args=["mcp"]`,
			"-c", "developer_instructions=" + tomlString(l.Context),
			l.Prompt}, nil
	case Copilot:
		p, err := mcpJSON()
		if err != nil {
			return nil, err
		}
		return []string{a.Path, "--additional-mcp-config", "@" + p, "-i", full}, nil
	case Gemini:
		return []string{a.Path, "-i", full}, nil
	case CursorAgent:
		return []string{a.Path, full}, nil
	case Cursor:
		return []string{"/usr/bin/open", "-a", a.Path, l.Dir}, nil
	}
	return nil, fmt.Errorf("%s cannot be started in a folder", a.Name)
}

// openCommand is /usr/bin/open (COVO_AGENTS_OPEN replaces it in tests).
func openCommand() string {
	if p := os.Getenv("COVO_AGENTS_OPEN"); p != "" {
		return p
	}
	return "/usr/bin/open"
}

// Start starts the agent: terminal agents in a new window of the user's
// terminal running a script in l.WorkDir; Cursor by opening the folder.
func Start(ctx context.Context, a Agent, l Launch) (Started, error) {
	st := Started{Agent: a.ID}
	argv, err := Command(a, l)
	if err != nil {
		return st, err
	}
	st.Command = argv
	if a.Kind == "app" {
		if argv[0] == "/usr/bin/open" {
			argv[0] = openCommand()
		}
		return st, run(ctx, argv)
	}
	if a.Kind != "terminal" {
		return st, fmt.Errorf("%s does not work in a folder", a.Name)
	}
	quoted := make([]string, len(argv))
	for i, s := range argv {
		quoted[i] = shellQuote(s)
	}
	script := "#!/bin/zsh -l\n" +
		"# Started by Covo for an agent session; deleted when the session closes.\n" +
		// Tell Covo when the agent is gone, also if the window is closed.
		"trap " + shellQuote(shellQuote(l.CovoBin)+" agent ended "+shellQuote(l.Session)+" >/dev/null 2>&1") + " EXIT HUP INT TERM\n" +
		"cd " + shellQuote(l.Dir) + " || exit 1\n" +
		"clear\n" +
		"printf '%s\\n\\n' " + shellQuote("Covo: "+a.Name+" in "+l.Dir+". Covo keeps the starting state: keep or undo the changes in Covo when you are done.") + "\n" +
		strings.Join(quoted, " ") + "\n"
	st.Script = filepath.Join(l.WorkDir, "start.command")
	if err := os.WriteFile(st.Script, []byte(script), 0o700); err != nil {
		return st, fmt.Errorf("write %s: %w", st.Script, err)
	}
	st.Terminal = l.Terminal
	var open []string
	switch strings.ToLower(l.Terminal) {
	case "ghostty":
		open = []string{openCommand(), "-na", "Ghostty", "--args", "-e", st.Script}
	case "iterm", "iterm2":
		open = []string{openCommand(), "-a", "iTerm", st.Script}
	default:
		st.Terminal = "Terminal"
		open = []string{openCommand(), "-a", "Terminal", st.Script}
	}
	return st, run(ctx, open)
}

func run(ctx context.Context, argv []string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", strings.Join(argv, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}
