// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/getcovo/covo/internal/errcode"
)

// Remote is a copy of a site on a server: production (pull only) or staging
// (pull and push).
type Remote struct {
	Site string `json:"site"`
	Name string `json:"name"`
	// Kind is production or staging.
	Kind string `json:"kind"`
	// Transport is ssh, sftp, ftp or ftps.
	Transport string `json:"transport"`
	Host      string `json:"host"`
	Port      int    `json:"port,omitempty"`
	User      string `json:"user"`
	// Path is the project folder on the server.
	Path string `json:"path"`
	// URL is the remote site's address.
	URL string `json:"url"`
	// Webroot is the folder served on the web, relative to Path ("" or
	// "public"): where the temporary bridge goes.
	Webroot string `json:"webroot,omitempty"`
	// Paths are the folders synced, relative to Path (default by framework).
	Paths []string `json:"paths"`
	// Excludes are rsync-style patterns never synced.
	Excludes []string `json:"excludes"`
}

const remoteCols = `site, name, kind, transport, host, port, user, path, url, webroot, paths, excludes`

func scanRemote(sc interface{ Scan(...any) error }) (Remote, error) {
	var r Remote
	var paths, excludes string
	if err := sc.Scan(&r.Site, &r.Name, &r.Kind, &r.Transport, &r.Host, &r.Port, &r.User, &r.Path, &r.URL, &r.Webroot, &paths, &excludes); err != nil {
		return r, err //nolint:wrapcheck // callers wrap
	}
	_ = json.Unmarshal([]byte(paths), &r.Paths)
	_ = json.Unmarshal([]byte(excludes), &r.Excludes)
	if r.Paths == nil {
		r.Paths = []string{}
	}
	if r.Excludes == nil {
		r.Excludes = []string{}
	}
	return r, nil
}

// PutRemote adds or replaces a remote.
func (s *Store) PutRemote(ctx context.Context, r Remote) error {
	paths, _ := json.Marshal(r.Paths)
	excludes, _ := json.Marshal(r.Excludes)
	_, err := s.db.ExecContext(ctx, `INSERT INTO site_remotes (`+remoteCols+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site, name) DO UPDATE SET kind = excluded.kind, transport = excluded.transport, host = excluded.host,
			port = excluded.port, user = excluded.user, path = excluded.path, url = excluded.url, webroot = excluded.webroot,
			paths = excluded.paths, excludes = excluded.excludes`,
		r.Site, r.Name, r.Kind, r.Transport, r.Host, r.Port, r.User, r.Path, r.URL, r.Webroot, string(paths), string(excludes))
	if err != nil {
		return fmt.Errorf("save remote %s of %s: %w", r.Name, r.Site, err)
	}
	return nil
}

// Remote returns one remote of a site.
func (s *Store) Remote(ctx context.Context, site, name string) (Remote, error) {
	r, err := scanRemote(s.db.QueryRowContext(ctx, `SELECT `+remoteCols+` FROM site_remotes WHERE site = ? AND name = ?`, site, name))
	if errors.Is(err, sql.ErrNoRows) {
		return r, errcode.Newf(errcode.NotFound, "%s has no remote %q (covo remote set)", site, name)
	}
	if err != nil {
		return r, fmt.Errorf("read remote: %w", err)
	}
	return r, nil
}

// ListRemotes returns a site's remotes.
func (s *Store) ListRemotes(ctx context.Context, site string) ([]Remote, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+remoteCols+` FROM site_remotes WHERE site = ? ORDER BY name`, site)
	if err != nil {
		return nil, fmt.Errorf("list remotes: %w", err)
	}
	defer rows.Close()
	out := []Remote{}
	for rows.Next() {
		r, err := scanRemote(rows)
		if err != nil {
			return nil, fmt.Errorf("scan remote: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list remotes: %w", err)
	}
	return out, nil
}

// DeleteRemote forgets a remote.
func (s *Store) DeleteRemote(ctx context.Context, site, name string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM site_remotes WHERE site = ? AND name = ?`, site, name); err != nil {
		return fmt.Errorf("delete remote: %w", err)
	}
	return nil
}
