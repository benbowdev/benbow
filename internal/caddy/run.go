// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package caddy

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
)

// Binary runs a Caddy executable against Benbow's data directory.
type Binary struct {
	Path    string
	DataDir string // XDG_DATA_HOME; Caddy's storage lives in DataDir/caddy
}

// Env returns the environment Caddy must run with so that it never writes
// outside the Benbow home (default is ~/Library/Application Support/Caddy).
func (b Binary) Env() map[string]string {
	return map[string]string{
		"XDG_DATA_HOME":   b.DataDir,
		"XDG_CONFIG_HOME": filepath.Join(b.DataDir, "config"),
	}
}

// Validate checks the Caddyfile at path.
func (b Binary) Validate(ctx context.Context, path string) error {
	_, err := b.run(ctx, "validate", "--config", path, "--adapter", "caddyfile")
	return err
}

// Reload sends the Caddyfile at path to the running Caddy via its admin socket.
func (b Binary) Reload(ctx context.Context, path, adminSocket string) error {
	_, err := b.run(ctx, "reload", "--config", path, "--adapter", "caddyfile", "--address", "unix/"+adminSocket)
	return err
}

func (b Binary) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, b.Path, args...)
	cmd.Env = os.Environ()
	for k, v := range b.Env() {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), errcode.New(errcode.Service,
			fmt.Errorf("caddy %s: %w: %s", args[0], err, lastError(out.String())))
	}
	return out.String(), nil
}

// lastError extracts the most useful line from Caddy's JSON log output.
func lastError(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}
