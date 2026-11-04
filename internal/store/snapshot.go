// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// Snapshot is a saved state of a site: its files and its database.
type Snapshot struct {
	ID        string    `json:"id"`
	Site      string    `json:"site"`
	CreatedAt time.Time `json:"created_at"`
	// Kind is manual, pre-restore, pre-pull, pre-migrate, mcp, session (an
	// agent session's starting point) or checkpoint (inside a session).
	Kind     string `json:"kind"`
	Note     string `json:"note,omitempty"`
	DBEngine string `json:"database_engine,omitempty"`
	DBName   string `json:"database,omitempty"`
	DBFile   string `json:"database_file,omitempty"`
	FilesDir string `json:"files_dir"`
	// Root is the project folder the files came from.
	Root string `json:"root"`
	// Bytes is the size of the database dump (the files are clones, which
	// take no space until they change).
	Bytes int64 `json:"bytes"`
	// SessionID links a session's starting snapshot and checkpoints to it.
	SessionID string `json:"session_id,omitempty"`
}

// AddSnapshot records a snapshot.
func (s *Store) AddSnapshot(ctx context.Context, sn Snapshot) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO snapshots (id, site, created_at, kind, note, db_engine, db_name, db_file, files_dir, root, bytes, session_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sn.ID, sn.Site, sn.CreatedAt.UTC().Format(time.RFC3339Nano), sn.Kind, sn.Note, sn.DBEngine, sn.DBName, sn.DBFile, sn.FilesDir, sn.Root, sn.Bytes, sn.SessionID)
	if err != nil {
		return fmt.Errorf("record snapshot %s of %s: %w", sn.ID, sn.Site, err)
	}
	return nil
}

func scanSnapshot(sc interface{ Scan(...any) error }) (Snapshot, error) {
	var sn Snapshot
	var at string
	err := sc.Scan(&sn.ID, &sn.Site, &at, &sn.Kind, &sn.Note, &sn.DBEngine, &sn.DBName, &sn.DBFile, &sn.FilesDir, &sn.Root, &sn.Bytes, &sn.SessionID)
	sn.CreatedAt, _ = time.Parse(time.RFC3339Nano, at)
	return sn, err
}

const snapshotCols = `id, site, created_at, kind, note, db_engine, db_name, db_file, files_dir, root, bytes, session_id`

// ListSnapshots returns a site's snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context, site string) ([]Snapshot, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE site = ? ORDER BY created_at DESC`, site)
	if err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	defer rows.Close()
	out := []Snapshot{}
	for rows.Next() {
		sn, err := scanSnapshot(rows)
		if err != nil {
			return nil, fmt.Errorf("scan snapshot: %w", err)
		}
		out = append(out, sn)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	return out, nil
}

// Snapshot returns one snapshot.
func (s *Store) Snapshot(ctx context.Context, site, id string) (Snapshot, error) {
	sn, err := scanSnapshot(s.db.QueryRowContext(ctx, `SELECT `+snapshotCols+` FROM snapshots WHERE site = ? AND id = ?`, site, id))
	if errors.Is(err, sql.ErrNoRows) {
		return sn, errcode.Newf(errcode.NotFound, "no snapshot %s of %s", id, site)
	}
	if err != nil {
		return sn, fmt.Errorf("read snapshot: %w", err)
	}
	return sn, nil
}

// DeleteSnapshot forgets a snapshot.
func (s *Store) DeleteSnapshot(ctx context.Context, site, id string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM snapshots WHERE site = ? AND id = ?`, site, id); err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	return nil
}

// UpdateSnapshotDB records a snapshot's database dump, saved after the
// files (an agent session's starting snapshot).
func (s *Store) UpdateSnapshotDB(ctx context.Context, sn Snapshot) error {
	_, err := s.db.ExecContext(ctx, `UPDATE snapshots SET db_engine = ?, db_name = ?, db_file = ?, bytes = ? WHERE site = ? AND id = ?`,
		sn.DBEngine, sn.DBName, sn.DBFile, sn.Bytes, sn.Site, sn.ID)
	if err != nil {
		return fmt.Errorf("update snapshot %s: %w", sn.ID, err)
	}
	return nil
}
