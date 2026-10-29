// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package store persists Covo state in SQLite (pure-Go driver, no CGO).
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	_ "modernc.org/sqlite" // registers the "sqlite" driver
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the Covo state database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and applies migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	// A single connection keeps SQLite semantics simple for a CLI.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error {
	if err := s.db.Close(); err != nil {
		return fmt.Errorf("close database: %w", err)
	}
	return nil
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current); err != nil {
		return fmt.Errorf("read schema version: %w", err)
	}
	files, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	sort.Strings(files)
	for _, name := range files {
		// Files are named NNNN_description.sql.
		base := strings.TrimPrefix(name, "migrations/")
		n, err := strconv.Atoi(strings.SplitN(base, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("bad migration name %s: %w", name, err)
		}
		if n <= current {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply migration %s: %w", name, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, n); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record migration %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %s: %w", name, err)
		}
	}
	return nil
}

// Setting returns the value of key and whether it is set.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read setting %s: %w", key, err)
	}
	return v, true, nil
}

// SetSetting stores value under key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	if err != nil {
		return fmt.Errorf("write setting %s: %w", key, err)
	}
	return nil
}

// DeleteSetting removes key. Removing a missing key is not an error.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return fmt.Errorf("delete setting %s: %w", key, err)
	}
	return nil
}

// PHPVersion is an installed PHP build.
type PHPVersion struct {
	Version     string    `json:"version"` // "8.4.16"
	Minor       string    `json:"minor"`   // "8.4"
	Path        string    `json:"path"`    // directory holding the binaries
	SHA256      string    `json:"sha256"`  // of the verified archive
	InstalledAt time.Time `json:"installed_at"`
}

// PutPHP records an installed PHP version, replacing any previous record.
func (s *Store) PutPHP(ctx context.Context, v PHPVersion) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO php_versions (version, minor, path, sha256, installed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(version) DO UPDATE SET minor = excluded.minor, path = excluded.path,
			sha256 = excluded.sha256, installed_at = excluded.installed_at`,
		v.Version, v.Minor, v.Path, v.SHA256, v.InstalledAt.UTC().Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("record php %s: %w", v.Version, err)
	}
	return nil
}

// ListPHP returns installed PHP versions ordered by version.
func (s *Store) ListPHP(ctx context.Context) ([]PHPVersion, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT version, minor, path, sha256, installed_at FROM php_versions`)
	if err != nil {
		return nil, fmt.Errorf("list php versions: %w", err)
	}
	defer rows.Close()
	var out []PHPVersion
	for rows.Next() {
		var v PHPVersion
		var at string
		if err := rows.Scan(&v.Version, &v.Minor, &v.Path, &v.SHA256, &at); err != nil {
			return nil, fmt.Errorf("scan php version: %w", err)
		}
		v.InstalledAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list php versions: %w", err)
	}
	sort.Slice(out, func(i, j int) bool { return CompareVersions(out[i].Version, out[j].Version) < 0 })
	return out, nil
}

// LatestPHP returns the newest installed patch release of minor (e.g. "8.4").
func (s *Store) LatestPHP(ctx context.Context, minor string) (PHPVersion, error) {
	all, err := s.ListPHP(ctx)
	if err != nil {
		return PHPVersion{}, err
	}
	for i := len(all) - 1; i >= 0; i-- {
		if all[i].Minor == minor {
			return all[i], nil
		}
	}
	return PHPVersion{}, errcode.Errorf(errcode.NotFound, "PHP "+minor+" is not installed")
}

// Site is a local site.
type Site struct {
	Name      string    `json:"name"`
	Path      string    `json:"path"`
	PHPMinor  string    `json:"php"`
	Domain    string    `json:"domain"`
	HTTPS     bool      `json:"https"`
	CreatedAt time.Time `json:"created_at"`
}

// AddSite inserts a site. Duplicate names or domains are a Conflict.
func (s *Store) AddSite(ctx context.Context, site Site) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sites (name, path, php_minor, domain, https, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		site.Name, site.Path, site.PHPMinor, site.Domain, site.HTTPS, site.CreatedAt.UTC().Format(time.RFC3339))
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", site.Name))
		}
		return fmt.Errorf("add site %s: %w", site.Name, err)
	}
	return nil
}

// Site returns the site called name.
func (s *Store) Site(ctx context.Context, name string) (Site, error) {
	sites, err := s.querySites(ctx, `WHERE name = ?`, name)
	if err != nil {
		return Site{}, err
	}
	if len(sites) == 0 {
		return Site{}, errcode.Errorf(errcode.NotFound, fmt.Sprintf("site %q not found", name))
	}
	return sites[0], nil
}

// ListSites returns all sites ordered by name.
func (s *Store) ListSites(ctx context.Context) ([]Site, error) {
	return s.querySites(ctx, `ORDER BY name`)
}

func (s *Store) querySites(ctx context.Context, tail string, args ...any) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, path, php_minor, domain, https, created_at FROM sites `+tail, args...)
	if err != nil {
		return nil, fmt.Errorf("query sites: %w", err)
	}
	defer rows.Close()
	out := []Site{}
	for rows.Next() {
		var site Site
		var at string
		if err := rows.Scan(&site.Name, &site.Path, &site.PHPMinor, &site.Domain, &site.HTTPS, &at); err != nil {
			return nil, fmt.Errorf("scan site: %w", err)
		}
		site.CreatedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("query sites: %w", err)
	}
	return out, nil
}

// RemoveSite deletes the site called name.
func (s *Store) RemoveSite(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sites WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("remove site %s: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errcode.Errorf(errcode.NotFound, fmt.Sprintf("site %q not found", name))
	}
	return nil
}

// CompareVersions compares dotted numeric versions ("8.4.16" vs "8.4.2").
func CompareVersions(a, b string) int {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}
