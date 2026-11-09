// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package agents knows the AI coding agents a user may already have
// (Claude Code, Codex, Gemini CLI, Cursor, GitHub Copilot CLI, Claude
// Desktop): where they are installed, whether Benbow's MCP server is
// connected to them, how to connect it (only in the user's own home, never
// in a project folder, always after a backup), and how to start them in a
// site's folder with Benbow connected for that run only. Benbow never installs
// an agent and never calls a model.
package agents

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/fsutil"
)

// Agent ids.
const (
	Claude        = "claude"
	Codex         = "codex"
	Gemini        = "gemini"
	CursorAgent   = "cursor-agent"
	Cursor        = "cursor"
	Copilot       = "copilot"
	ClaudeDesktop = "claude-desktop"
)

// ServerName is the name Benbow's MCP server has in agents' configurations.
const ServerName = "benbow"

// Agent is one agent and what Benbow knows about it.
type Agent struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is terminal (a CLI started in a terminal), app (opens the
	// folder) or desktop (Claude Desktop: no folder, MCP only).
	Kind      string `json:"kind"`
	Installed bool   `json:"installed"`
	Path      string `json:"path,omitempty"`
	// Connected reports a global entry for Benbow in the agent's own
	// configuration (the user's home).
	Connected bool `json:"connected"`
	// ConfigPath is the file that entry lives in.
	ConfigPath string `json:"config_path,omitempty"`
	// PerRun is set when Benbow can connect itself just for one run started
	// from Benbow, writing no configuration.
	PerRun bool `json:"per_run"`
	// CanConnect is set when Benbow can add the global entry.
	CanConnect bool `json:"can_connect"`
}

type spec struct {
	id, name, kind string
	bins           []string // names on PATH, in order of preference
	apps           []string // app bundles (relative to /Applications)
	// config is the global configuration file, relative to home.
	config string
	// perRun: Benbow can pass its MCP server on the command line.
	perRun bool
}

var specs = []spec{
	{id: Claude, name: "Claude Code", kind: "terminal", bins: []string{"claude"}, config: ".claude.json", perRun: true},
	{id: Codex, name: "Codex", kind: "terminal", bins: []string{"codex"}, config: ".codex/config.toml", perRun: true},
	{id: Gemini, name: "Gemini CLI", kind: "terminal", bins: []string{"gemini"}, config: ".gemini/settings.json"},
	{id: CursorAgent, name: "Cursor Agent (terminal)", kind: "terminal", bins: []string{"cursor-agent", "agent"}, config: ".cursor/mcp.json"},
	{id: Cursor, name: "Cursor", kind: "app", apps: []string{"Cursor.app"}, config: ".cursor/mcp.json"},
	{id: Copilot, name: "GitHub Copilot CLI", kind: "terminal", bins: []string{"copilot"}, config: ".copilot/mcp-config.json", perRun: true},
	{id: ClaudeDesktop, name: "Claude Desktop", kind: "desktop", apps: []string{"Claude.app"}, config: "Library/Application Support/Claude/claude_desktop_config.json"},
}

func specFor(id string) (spec, bool) {
	i := slices.IndexFunc(specs, func(s spec) bool { return s.id == id })
	if i < 0 {
		return spec{}, false
	}
	return specs[i], true
}

// Env is where to look: the user's home and how to find programs. Tests
// point it at fakes.
type Env struct {
	Home string
	// Path, when set, is searched instead of the login shell's PATH.
	Path string
	// Apps are the application folders (default /Applications and
	// ~/Applications).
	Apps []string
}

// DefaultEnv is the real user's environment (BENBOW_AGENTS_HOME and
// BENBOW_AGENTS_PATH override it, for tests).
func DefaultEnv() Env {
	home, _ := os.UserHomeDir()
	e := Env{Home: home, Apps: []string{"/Applications", filepath.Join(home, "Applications")}}
	if h := os.Getenv("BENBOW_AGENTS_HOME"); h != "" {
		e.Home, e.Apps = h, []string{filepath.Join(h, "Applications")}
	}
	if p := os.Getenv("BENBOW_AGENTS_PATH"); p != "" {
		e.Path = p
	}
	return e
}

// Detect lists every known agent.
func Detect(ctx context.Context, env Env) []Agent {
	found := env.findBinaries(ctx)
	out := make([]Agent, 0, len(specs))
	for _, s := range specs {
		a := Agent{ID: s.id, Name: s.name, Kind: s.kind, PerRun: s.perRun, ConfigPath: filepath.Join(env.Home, s.config)}
		for _, b := range s.bins {
			if p := found[b]; p != "" {
				a.Path = p
				break
			}
		}
		if a.Path == "" && s.id == Codex {
			a.Path = env.bundledCodex()
		}
		for _, app := range s.apps {
			for _, dir := range env.Apps {
				if fi, err := os.Stat(filepath.Join(dir, app)); err == nil && fi.IsDir() {
					a.Path = filepath.Join(dir, app)
				}
			}
		}
		a.Installed = a.Path != ""
		a.Connected = connected(s, a.ConfigPath)
		a.CanConnect = a.Installed && s.id != Copilot
		out = append(out, a)
	}
	return out
}

// Find returns one agent.
func Find(ctx context.Context, env Env, id string) (Agent, error) {
	for _, a := range Detect(ctx, env) {
		if a.ID == id {
			return a, nil
		}
	}
	return Agent{}, fmt.Errorf("unknown agent %q", id)
}

// commonDirs are where agents' installers put their commands, searched
// when the login shell does not answer.
func (e Env) commonDirs() []string {
	dirs := []string{
		filepath.Join(e.Home, ".local/bin"), "/opt/homebrew/bin", "/usr/local/bin",
		filepath.Join(e.Home, ".volta/bin"), filepath.Join(e.Home, ".bun/bin"), filepath.Join(e.Home, ".npm-global/bin"),
		filepath.Join(e.Home, ".claude/local"),
	}
	nvm, _ := filepath.Glob(filepath.Join(e.Home, ".nvm/versions/node/*/bin"))
	slices.Sort(nvm)
	slices.Reverse(nvm) // newest Node first
	return append(dirs, nvm...)
}

var reWord = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

// findBinaries looks every agent command up: on the login shell's PATH (a
// GUI app's own PATH is minimal), then in the usual install folders.
// Broken links are skipped.
func (e Env) findBinaries(ctx context.Context) map[string]string {
	var names []string
	for _, s := range specs {
		names = append(names, s.bins...)
	}
	found := map[string]string{}
	var dirs []string
	if e.Path != "" {
		dirs = filepath.SplitList(e.Path)
	} else {
		dirs = append(loginPath(ctx), e.commonDirs()...)
	}
	for _, n := range names {
		if !reWord.MatchString(n) {
			continue
		}
		for _, d := range dirs {
			p := filepath.Join(d, n)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
				found[n] = p
				break
			}
		}
	}
	return found
}

// loginPath is the PATH of the user's login shell (nvm, Homebrew and
// installers usually set it in .zprofile).
func loginPath(ctx context.Context) []string {
	shell := os.Getenv("SHELL")
	if shell == "" || !filepath.IsAbs(shell) {
		shell = "/bin/zsh"
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, shell, "-l", "-c", `printf '%s' "$PATH"`)
	cmd.Stdin = nil
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	return filepath.SplitList(strings.TrimSpace(string(out)))
}

func (e Env) bundledCodex() string {
	for _, dir := range e.Apps {
		m, _ := filepath.Glob(filepath.Join(dir, "ChatGPT*.app/Contents/Resources/codex-cli/bin/codex"))
		if len(m) > 0 {
			return m[0]
		}
	}
	return ""
}

// connected reads the agent's global configuration for a "benbow" server.
func connected(s spec, path string) bool { return hasServer(s, path, ServerName) }

// hasServer reads the agent's global configuration for a server named name.
func hasServer(s spec, path, name string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	if s.id == Codex {
		return regexp.MustCompile(`(?m)^\s*\[mcp_servers\.` + regexp.QuoteMeta(name) + `\]\s*$`).Match(b)
	}
	var cfg struct {
		MCPServers map[string]json.RawMessage `json:"mcpServers"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return false
	}
	_, ok := cfg.MCPServers[name]
	return ok
}

// ConnectResult is what Connect changed.
type ConnectResult struct {
	Agent      string `json:"agent"`
	ConfigPath string `json:"config_path"`
	Backup     string `json:"backup,omitempty"`
	// Command is the agent's own command Benbow ran, if any.
	Command string `json:"command,omitempty"`
	Already bool   `json:"already,omitempty"`
	// Restart is set when the agent must be restarted to see Benbow.
	Restart bool `json:"restart,omitempty"`
}

// Connect adds Benbow's MCP server (benbowBin mcp) to an agent's global
// configuration in the user's home, after a backup into backupDir. Claude
// Code and Codex do it with their own command.
func Connect(ctx context.Context, env Env, id, benbowBin, backupDir string) (ConnectResult, error) {
	res := ConnectResult{Agent: id}
	s, ok := specFor(id)
	if !ok {
		return res, fmt.Errorf("unknown agent %q", id)
	}
	a, err := Find(ctx, env, id)
	if err != nil {
		return res, err
	}
	res.ConfigPath = a.ConfigPath
	if !a.Installed {
		return res, fmt.Errorf("%s is not installed on this Mac", s.name)
	}
	if !a.CanConnect {
		return res, fmt.Errorf("%s gets Benbow when started from Benbow; there is no global entry to add", s.name)
	}
	if a.Connected {
		res.Already = true
		return res, nil
	}
	if res.Backup, err = backup(a.ConfigPath, backupDir); err != nil {
		return res, err
	}
	switch id {
	case Claude, Codex:
		args := []string{"mcp", "add"}
		if id == Claude {
			args = append(args, "-s", "user")
		}
		args = append(args, ServerName, "--", benbowBin, "mcp")
		res.Command = a.Path + " " + strings.Join(args, " ")
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, a.Path, args...)
		cmd.Dir = env.Home
		cmd.Env = append(os.Environ(), "HOME="+env.Home)
		if out, err := cmd.CombinedOutput(); err != nil {
			return res, fmt.Errorf("%s: %w: %s", res.Command, err, strings.TrimSpace(string(out)))
		}
	default:
		entry := map[string]any{"command": benbowBin, "args": []string{"mcp"}}
		if err := addJSONServer(a.ConfigPath, entry); err != nil {
			return res, err
		}
	}
	res.Restart = id == ClaudeDesktop || id == Cursor
	return res, nil
}

// backup copies path (if it exists) into dir with a timestamp.
func backup(path, dir string) (string, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}
	dst := filepath.Join(dir, filepath.Base(path)+"."+time.Now().Format("20060102-150405"))
	if err := os.WriteFile(dst, b, 0o600); err != nil {
		return "", fmt.Errorf("back up %s: %w", path, err)
	}
	return dst, nil
}

// addJSONServer adds mcpServers.benbow to a JSON configuration, keeping
// everything else (key order may change; the backup keeps the original).
func addJSONServer(path string, entry map[string]any) error {
	cfg := map[string]any{}
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return fmt.Errorf("read %s: %w", path, err)
	case len(bytes.TrimSpace(b)) > 0:
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("%s is not valid JSON, so Benbow leaves it alone: %w", path, err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	servers[ServerName] = entry
	cfg["mcpServers"] = servers
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}
	return writeKeepingMode(path, append(out, '\n'))
}

// writeKeepingMode replaces path atomically, keeping its permissions.
func writeKeepingMode(path string, body []byte) error {
	mode := fs.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	_, err := fsutil.WriteAtomic(path, body, mode)
	return err
}

// Disconnect removes Benbow's MCP server from an agent's global
// configuration (benbow uninstall). Claude Code and Codex do it with their
// own command when it is there; JSON files lose only the "benbow" entry.
func Disconnect(ctx context.Context, env Env, id string) error {
	return RemoveServer(ctx, env, id, ServerName)
}

// HasServer reports whether an agent's global configuration has an MCP
// server named name (the product's former name, when migrating).
func HasServer(ctx context.Context, env Env, id, name string) bool {
	a, err := Find(ctx, env, id)
	if err != nil {
		return false
	}
	s, _ := specFor(id)
	return hasServer(s, a.ConfigPath, name)
}

// RemoveServer removes the MCP server named name from an agent's global
// configuration, with the agent's own command when it has one.
func RemoveServer(ctx context.Context, env Env, id, name string) error {
	a, err := Find(ctx, env, id)
	if err != nil {
		return err
	}
	s, _ := specFor(id)
	if !hasServer(s, a.ConfigPath, name) {
		return nil
	}
	if (id == Claude || id == Codex) && a.Path != "" {
		args := []string{"mcp", "remove"}
		if id == Claude {
			args = append(args, "-s", "user")
		}
		args = append(args, name)
		ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, a.Path, args...)
		cmd.Dir = env.Home
		cmd.Env = append(os.Environ(), "HOME="+env.Home)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("%s %s: %w: %s", a.Path, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	if id == Codex {
		return fmt.Errorf("remove the [mcp_servers.%s] table from %s by hand (codex is not installed)", name, a.ConfigPath)
	}
	return removeJSONServer(a.ConfigPath, name)
}

// removeJSONServer deletes mcpServers.<name> from a JSON configuration.
func removeJSONServer(path, name string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	cfg := map[string]any{}
	if err := json.Unmarshal(b, &cfg); err != nil {
		return fmt.Errorf("%s is not valid JSON, so Benbow leaves it alone: %w", path, err)
	}
	servers, _ := cfg["mcpServers"].(map[string]any)
	if _, ok := servers[name]; !ok {
		return nil
	}
	delete(servers, name)
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	return writeKeepingMode(path, append(out, '\n'))
}
