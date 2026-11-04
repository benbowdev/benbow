// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
)

// Group organizes sites in the list. Groups nest to any depth; they change
// nothing about how sites are served.
type Group struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	// ParentID is the enclosing group, or 0 at the top level.
	ParentID int64 `json:"parent_id,omitempty"`
	// Position orders groups with the same parent (0: by name).
	Position  int       `json:"position,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// nextPosition is the position for a new group or site at the end of a
// level (groups and sites share the positions of a level): 0 while the
// level is still ordered by name, after the last item otherwise. It takes
// the level's group id twice.
const nextPosition = `(SELECT CASE WHEN MAX(p) > 0 THEN MAX(p) + 1 ELSE 0 END FROM
	(SELECT position AS p FROM sites WHERE group_id IS ? UNION ALL SELECT position FROM site_groups WHERE parent_id IS ?))`

// nullID stores 0 as NULL (no group).
func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// AddGroup inserts a group and returns its ID.
func (s *Store) AddGroup(ctx context.Context, g Group) (int64, error) {
	res, err := s.db.ExecContext(ctx, `INSERT INTO site_groups (name, parent_id, created_at, position) VALUES (?, ?, ?, `+nextPosition+`)`,
		g.Name, nullID(g.ParentID), g.CreatedAt.UTC().Format(time.RFC3339), nullID(g.ParentID), nullID(g.ParentID))
	if err != nil {
		if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
			return 0, errcode.Newf(errcode.NotFound, "group %d not found", g.ParentID)
		}
		return 0, fmt.Errorf("add group %s: %w", g.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("add group %s: %w", g.Name, err)
	}
	return id, nil
}

// ListGroups returns every group in list order (position, then name).
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, parent_id, created_at, position FROM site_groups ORDER BY position, name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var parent sql.NullInt64
		var at string
		if err := rows.Scan(&g.ID, &g.Name, &parent, &at, &g.Position); err != nil {
			return nil, fmt.Errorf("scan group: %w", err)
		}
		g.ParentID = parent.Int64
		g.CreatedAt, _ = time.Parse(time.RFC3339, at)
		out = append(out, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return out, nil
}

// Group returns the group with id.
func (s *Store) Group(ctx context.Context, id int64) (Group, error) {
	var g Group
	var parent sql.NullInt64
	var at string
	err := s.db.QueryRowContext(ctx, `SELECT id, name, parent_id, created_at, position FROM site_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &parent, &at, &g.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return g, errcode.Newf(errcode.NotFound, "group %d not found", id)
	}
	if err != nil {
		return g, fmt.Errorf("read group %d: %w", id, err)
	}
	g.ParentID = parent.Int64
	g.CreatedAt, _ = time.Parse(time.RFC3339, at)
	return g, nil
}

// UpdateGroup renames or moves a group.
func (s *Store) UpdateGroup(ctx context.Context, g Group) error {
	res, err := s.db.ExecContext(ctx, `UPDATE site_groups SET name = ?, parent_id = ? WHERE id = ?`, g.Name, nullID(g.ParentID), g.ID)
	if err != nil {
		return fmt.Errorf("update group %d: %w", g.ID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errcode.Newf(errcode.NotFound, "group %d not found", g.ID)
	}
	return nil
}

// RemoveGroup deletes a group, moving its sites and subgroups to its parent.
func (s *Store) RemoveGroup(ctx context.Context, id int64) error {
	g, err := s.Group(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("remove group %d: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`UPDATE sites SET group_id = ? WHERE group_id = ?`,
		`UPDATE site_groups SET parent_id = ? WHERE parent_id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, nullID(g.ParentID), id); err != nil {
			return fmt.Errorf("remove group %d: %w", id, err)
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM site_groups WHERE id = ?`, id); err != nil {
		return fmt.Errorf("remove group %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("remove group %d: %w", id, err)
	}
	return nil
}

// OrderItem is a group (Group set) or a site (Site set) of a level.
type OrderItem struct {
	Group int64
	Site  string
}

// SetOrder puts items inside parent (0: top level) at positions 1, 2, …
// Groups and sites share the positions of a level. Items not listed keep
// their position.
func (s *Store) SetOrder(ctx context.Context, parent int64, items []OrderItem) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("order the list: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for i, it := range items {
		if it.Site != "" {
			_, err = tx.ExecContext(ctx, `UPDATE sites SET group_id = ?, position = ? WHERE name = ?`, nullID(parent), i+1, it.Site)
		} else {
			_, err = tx.ExecContext(ctx, `UPDATE site_groups SET parent_id = ?, position = ? WHERE id = ?`, nullID(parent), i+1, it.Group)
		}
		if err != nil {
			return fmt.Errorf("order the list: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("order the list: %w", err)
	}
	return nil
}
