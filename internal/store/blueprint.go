// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
)

// Blueprint is a saved site template; Data is its JSON.
type Blueprint struct {
	Name    string    `json:"name"`
	Data    string    `json:"-"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// BlueprintSource is the SHA-256 Benbow saw the first time it downloaded a
// theme or plugin from URL.
type BlueprintSource struct {
	URL       string    `json:"url"`
	SHA256    string    `json:"sha256"`
	FirstUsed time.Time `json:"first_used"`
	LastUsed  time.Time `json:"last_used"`
}

// SaveBlueprint adds or replaces a blueprint.
func (s *Store) SaveBlueprint(ctx context.Context, name, data string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO blueprints (name, data, created, updated) VALUES (?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET data = excluded.data, updated = excluded.updated`, name, data, now, now)
	if err != nil {
		return fmt.Errorf("save blueprint: %w", err)
	}
	return nil
}

// Blueprint reads one blueprint.
func (s *Store) Blueprint(ctx context.Context, name string) (Blueprint, error) {
	var b Blueprint
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT name, data, created, updated FROM blueprints WHERE name = ?`, name).
		Scan(&b.Name, &b.Data, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return b, errcode.Newf(errcode.NotFound, "blueprint %q not found", name)
	}
	if err != nil {
		return b, fmt.Errorf("read blueprint: %w", err)
	}
	b.Created, _ = time.Parse(time.RFC3339Nano, created)
	b.Updated, _ = time.Parse(time.RFC3339Nano, updated)
	return b, nil
}

// Blueprints lists every blueprint by name.
func (s *Store) Blueprints(ctx context.Context) ([]Blueprint, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, data, created, updated FROM blueprints ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list blueprints: %w", err)
	}
	defer rows.Close()
	out := []Blueprint{}
	for rows.Next() {
		var b Blueprint
		var created, updated string
		if err := rows.Scan(&b.Name, &b.Data, &created, &updated); err != nil {
			return nil, fmt.Errorf("list blueprints: %w", err)
		}
		b.Created, _ = time.Parse(time.RFC3339Nano, created)
		b.Updated, _ = time.Parse(time.RFC3339Nano, updated)
		out = append(out, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list blueprints: %w", err)
	}
	return out, nil
}

// DeleteBlueprint removes a blueprint.
func (s *Store) DeleteBlueprint(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM blueprints WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("delete blueprint: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errcode.Newf(errcode.NotFound, "blueprint %q not found", name)
	}
	return nil
}

// BlueprintSourceFor returns what Benbow recorded for url; ok is false the
// first time.
func (s *Store) BlueprintSourceFor(ctx context.Context, url string) (src BlueprintSource, ok bool, err error) {
	var first, last string
	err = s.db.QueryRowContext(ctx, `SELECT url, sha256, first_used, last_used FROM blueprint_sources WHERE url = ?`, url).
		Scan(&src.URL, &src.SHA256, &first, &last)
	if errors.Is(err, sql.ErrNoRows) {
		return src, false, nil
	}
	if err != nil {
		return src, false, fmt.Errorf("read blueprint source: %w", err)
	}
	src.FirstUsed, _ = time.Parse(time.RFC3339Nano, first)
	src.LastUsed, _ = time.Parse(time.RFC3339Nano, last)
	return src, true, nil
}

// RecordBlueprintSource stores (or replaces) the SHA-256 of url.
func (s *Store) RecordBlueprintSource(ctx context.Context, url, sha string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `INSERT INTO blueprint_sources (url, sha256, first_used, last_used) VALUES (?, ?, ?, ?)
		ON CONFLICT(url) DO UPDATE SET sha256 = excluded.sha256, last_used = excluded.last_used`, url, sha, now, now)
	if err != nil {
		return fmt.Errorf("record blueprint source: %w", err)
	}
	return nil
}

// BlueprintSources lists every recorded source.
func (s *Store) BlueprintSources(ctx context.Context) ([]BlueprintSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT url, sha256, first_used, last_used FROM blueprint_sources ORDER BY url`)
	if err != nil {
		return nil, fmt.Errorf("list blueprint sources: %w", err)
	}
	defer rows.Close()
	out := []BlueprintSource{}
	for rows.Next() {
		var src BlueprintSource
		var first, last string
		if err := rows.Scan(&src.URL, &src.SHA256, &first, &last); err != nil {
			return nil, fmt.Errorf("list blueprint sources: %w", err)
		}
		src.FirstUsed, _ = time.Parse(time.RFC3339Nano, first)
		src.LastUsed, _ = time.Parse(time.RFC3339Nano, last)
		out = append(out, src)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list blueprint sources: %w", err)
	}
	return out, nil
}
