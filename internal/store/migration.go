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

// Migration records what an assisted migration did to a site, so that it
// can be undone.
type Migration struct {
	Site   string `json:"site"`
	Source string `json:"source"`
	DBName string `json:"database"`
	DBUser string `json:"database_user,omitempty"`
	// CreatedDB is true when the migration created the database (undo
	// deletes it).
	CreatedDB  bool      `json:"created_database"`
	ConfigPath string    `json:"config_path,omitempty"`
	BackupPath string    `json:"backup_path,omitempty"`
	URLFrom    string    `json:"url_from,omitempty"`
	URLTo      string    `json:"url_to,omitempty"`
	At         time.Time `json:"at"`
}

// PutMigration records (or replaces) a site's migration.
func (s *Store) PutMigration(ctx context.Context, m Migration) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO site_migrations (site, source, db_name, db_user, created_db, config_path, backup_path, url_from, url_to, at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(site) DO UPDATE SET source = excluded.source, db_name = excluded.db_name, db_user = excluded.db_user,
			created_db = excluded.created_db, config_path = excluded.config_path, backup_path = excluded.backup_path,
			url_from = excluded.url_from, url_to = excluded.url_to, at = excluded.at`,
		m.Site, m.Source, m.DBName, m.DBUser, m.CreatedDB, m.ConfigPath, m.BackupPath, m.URLFrom, m.URLTo, m.At.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record migration of %s: %w", m.Site, err)
	}
	return nil
}

// Migration returns a site's migration.
func (s *Store) Migration(ctx context.Context, site string) (Migration, error) {
	var m Migration
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT site, source, db_name, db_user, created_db, config_path, backup_path, url_from, url_to, at
		FROM site_migrations WHERE site = ?`, site).
		Scan(&m.Site, &m.Source, &m.DBName, &m.DBUser, &m.CreatedDB, &m.ConfigPath, &m.BackupPath, &m.URLFrom, &m.URLTo, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return m, errcode.Errorf(errcode.NotFound, fmt.Sprintf("%s was not migrated by Covo", site))
	}
	if err != nil {
		return m, fmt.Errorf("read migration of %s: %w", site, err)
	}
	m.At, _ = time.Parse(time.RFC3339, at)
	return m, nil
}

// DeleteMigration forgets a site's migration.
func (s *Store) DeleteMigration(ctx context.Context, site string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM site_migrations WHERE site = ?`, site); err != nil {
		return fmt.Errorf("forget migration of %s: %w", site, err)
	}
	return nil
}

// ListMigrations returns every site's migration, by site.
func (s *Store) ListMigrations(ctx context.Context) (map[string]Migration, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT site, source, db_name, db_user, created_db, config_path, backup_path, url_from, url_to, at FROM site_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()
	out := map[string]Migration{}
	for rows.Next() {
		var m Migration
		var at string
		if err := rows.Scan(&m.Site, &m.Source, &m.DBName, &m.DBUser, &m.CreatedDB, &m.ConfigPath, &m.BackupPath, &m.URLFrom, &m.URLTo, &at); err != nil {
			return nil, fmt.Errorf("scan migration: %w", err)
		}
		m.At, _ = time.Parse(time.RFC3339, at)
		out[m.Site] = m
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	return out, nil
}
