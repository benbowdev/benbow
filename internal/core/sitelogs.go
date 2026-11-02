// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// Site log kinds.
const (
	LogAccess = "access" // requests, written by Caddy
	LogPHP    = "php"    // PHP errors, warnings and notices
)

const (
	defaultLogLines = 200
	maxLogLines     = 2000
	// maxLogTail bounds how much of a log is read to find the last lines.
	maxLogTail = 1 << 20
)

// SiteLog is the end of one of a site's logs.
type SiteLog struct {
	Site  string   `json:"site"`
	Kind  string   `json:"kind"`
	Path  string   `json:"path"`
	Size  int64    `json:"size"`
	Lines []string `json:"lines"`
}

func (c *Core) siteLogDir(name string) string { return filepath.Join(c.Layout.Logs, "sites", name) }

func (c *Core) siteLogPath(name, kind string) string {
	file := "access.log"
	if kind == LogPHP {
		file = "php-error.log"
	}
	return filepath.Join(c.siteLogDir(name), file)
}

// SiteLogs returns the last lines (default 200) of a site's access or PHP
// error log. Access lines are condensed from Caddy's JSON.
func (c *Core) SiteLogs(ctx context.Context, name, kind string, lines int) (SiteLog, error) {
	if kind != LogAccess && kind != LogPHP {
		return SiteLog{}, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("log kind must be %s or %s, got %q", LogAccess, LogPHP, kind))
	}
	if lines <= 0 {
		lines = defaultLogLines
	}
	lines = min(lines, maxLogLines)
	if _, err := c.Store.Site(ctx, name); err != nil {
		return SiteLog{}, err
	}
	out := SiteLog{Site: name, Kind: kind, Path: c.siteLogPath(name, kind), Lines: []string{}}
	raw, size, err := tailLines(out.Path, lines)
	if err != nil {
		return out, err
	}
	out.Size = size
	for _, l := range raw {
		if kind == LogAccess {
			l = formatAccess(l)
		}
		out.Lines = append(out.Lines, l)
	}
	return out, nil
}

// ClearSiteLogs empties a site's logs.
func (c *Core) ClearSiteLogs(ctx context.Context, name string) (SiteLog, error) {
	if _, err := c.Store.Site(ctx, name); err != nil {
		return SiteLog{}, err
	}
	for _, kind := range []string{LogAccess, LogPHP} {
		// Truncate rather than delete: Caddy keeps the access log open.
		if err := os.Truncate(c.siteLogPath(name, kind), 0); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return SiteLog{}, fmt.Errorf("clear %s log of %s: %w", kind, name, err)
		}
	}
	return SiteLog{Site: name, Lines: []string{}}, nil
}

// ensureSiteLogDirs creates the log folder of every served site.
func (c *Core) ensureSiteLogDirs(names []string) error {
	for _, n := range names {
		if err := os.MkdirAll(c.siteLogDir(n), 0o700); err != nil {
			return fmt.Errorf("create log folder for %s: %w", n, err)
		}
	}
	return nil
}

// tailLines returns the last n lines of path (nothing if it does not exist).
func tailLines(path string, n int) ([]string, int64, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, fmt.Errorf("open log: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("stat log: %w", err)
	}
	start := max(info.Size()-maxLogTail, 0)
	buf := make([]byte, info.Size()-start)
	if _, err := f.ReadAt(buf, start); err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, fmt.Errorf("read log: %w", err)
	}
	text := strings.TrimRight(string(buf), "\n")
	if text == "" {
		return nil, info.Size(), nil
	}
	all := strings.Split(text, "\n")
	if start > 0 {
		all = all[1:] // probably cut in the middle
	}
	return all[max(len(all)-n, 0):], info.Size(), nil
}

// formatAccess turns one Caddy JSON access entry into
// "2026-11-02 21:40:01  200  GET  demo.localhost/path  12 ms".
func formatAccess(line string) string {
	var e struct {
		TS       float64 `json:"ts"`
		Status   int     `json:"status"`
		Duration float64 `json:"duration"`
		Request  struct {
			Method string `json:"method"`
			Host   string `json:"host"`
			URI    string `json:"uri"`
		} `json:"request"`
	}
	if err := json.Unmarshal([]byte(line), &e); err != nil || e.Request.Method == "" {
		return line
	}
	sec, frac := math.Modf(e.TS)
	ts := time.Unix(int64(sec), int64(frac*1e9)).Local().Format("2006-01-02 15:04:05")
	host, _, _ := strings.Cut(e.Request.Host, ":")
	return fmt.Sprintf("%s  %d  %s  %s%s  %d ms", ts, e.Status, e.Request.Method, host, e.Request.URI, int(e.Duration*1000))
}
