// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/postgres"
	"github.com/getcovo/covo/internal/store"
)

// Snapshots save a site's project folder and database under the Covo
// folder. Files are cloned with APFS clonefile(2): a snapshot takes no
// space until the files change. .git and node_modules are left out: a
// restore never rewinds git history or installed packages.

// Snapshot kinds.
const (
	SnapshotManual     = "manual"
	SnapshotPreRestore = "pre-restore"
	SnapshotPrePull    = "pre-pull"
	SnapshotMCP        = "mcp"
)

const (
	settingSnapshotKeep = "snapshot_keep"
	defaultSnapshotKeep = 10
)

// snapshotSkip are folder names never saved nor restored.
var snapshotSkip = map[string]bool{".git": true, "node_modules": true}

func (c *Core) snapshotDir(site string) string {
	return filepath.Join(c.Layout.Data, "snapshots", site)
}

// SnapshotKeep returns how many snapshots are kept per site.
func (c *Core) SnapshotKeep(ctx context.Context) (int, error) {
	v, ok, err := c.Store.Setting(ctx, settingSnapshotKeep)
	if err != nil || !ok {
		return defaultSnapshotKeep, err
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 {
		return defaultSnapshotKeep, nil //nolint:nilerr // a bad value means the default
	}
	return n, nil
}

// SetSnapshotKeep sets how many snapshots are kept per site (1–100).
func (c *Core) SetSnapshotKeep(ctx context.Context, n int) error {
	if n < 1 || n > 100 {
		return errcode.Errorf(errcode.InvalidInput, "keep between 1 and 100 snapshots per site")
	}
	return c.Store.SetSetting(ctx, settingSnapshotKeep, strconv.Itoa(n))
}

// CreateSnapshot saves a site's files and database.
func (c *Core) CreateSnapshot(ctx context.Context, name, kind, note string) (store.Snapshot, error) {
	return c.createSnapshot(ctx, name, kind, note, "")
}

// createSnapshot never prunes the snapshot keep (the one being restored).
func (c *Core) createSnapshot(ctx context.Context, name, kind, note, keep string) (store.Snapshot, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return store.Snapshot{}, err
	}
	if kind == "" {
		kind = SnapshotManual
	}
	now := time.Now()
	sn := store.Snapshot{
		ID: now.Format("20060102-150405.000"), Site: name, CreatedAt: now, Kind: kind, Note: note, Root: projectRoot(site),
	}
	dir := filepath.Join(c.snapshotDir(name), sn.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return sn, fmt.Errorf("create %s: %w", dir, err)
	}
	fail := func(err error) (store.Snapshot, error) {
		_ = os.RemoveAll(dir)
		return store.Snapshot{}, err
	}
	sn.FilesDir = filepath.Join(dir, "files")
	if err := cloneTree(sn.Root, sn.FilesDir); err != nil {
		return fail(err)
	}
	if site.Database != "" {
		sn.DBEngine, sn.DBName = engineOf(site), site.Database
		if sn.DBEngine == EnginePostgres {
			sn.DBFile = filepath.Join(dir, "database.pgdump")
			err = c.dumpPostgres(ctx, site.Database, sn.DBFile)
		} else {
			sn.DBFile = filepath.Join(dir, "database.sql.gz")
			err = c.dumpMySQL(ctx, site.Database, sn.DBFile)
		}
		if err != nil {
			return fail(err)
		}
		if fi, err := os.Stat(sn.DBFile); err == nil {
			sn.Bytes = fi.Size()
		}
	}
	if err := c.Store.AddSnapshot(ctx, sn); err != nil {
		return fail(err)
	}
	c.pruneSnapshots(ctx, name, keep)
	return sn, nil
}

// pruneSnapshots deletes the oldest snapshots beyond the limit.
func (c *Core) pruneSnapshots(ctx context.Context, name, spare string) {
	keep, _ := c.SnapshotKeep(ctx)
	all, err := c.Store.ListSnapshots(ctx, name)
	if err != nil {
		return
	}
	for _, sn := range all[min(len(all), keep):] {
		if sn.ID != spare {
			_ = c.removeSnapshot(ctx, sn)
		}
	}
}

func (c *Core) removeSnapshot(ctx context.Context, sn store.Snapshot) error {
	if err := os.RemoveAll(filepath.Dir(sn.FilesDir)); err != nil {
		return fmt.Errorf("remove snapshot %s: %w", sn.ID, err)
	}
	return c.Store.DeleteSnapshot(ctx, sn.Site, sn.ID)
}

// ListSnapshots returns a site's snapshots, newest first.
func (c *Core) ListSnapshots(ctx context.Context, name string) ([]store.Snapshot, error) {
	if _, err := c.Store.Site(ctx, name); err != nil {
		return nil, err
	}
	return c.Store.ListSnapshots(ctx, name)
}

// DeleteSnapshot deletes one snapshot.
func (c *Core) DeleteSnapshot(ctx context.Context, name, id string) error {
	sn, err := c.Store.Snapshot(ctx, name, id)
	if err != nil {
		return err
	}
	return c.removeSnapshot(ctx, sn)
}

// RestoreResult is a restore and the snapshot taken just before it.
type RestoreResult struct {
	Restored store.Snapshot `json:"restored"`
	// Before is the state just before the restore: restoring it undoes it.
	Before store.Snapshot `json:"before"`
}

// RestoreSnapshot puts a site's files and database back as they were in a
// snapshot. The current state is saved first (a pre-restore snapshot), and
// if any step fails it is put back.
func (c *Core) RestoreSnapshot(ctx context.Context, name, id string) (RestoreResult, error) {
	var res RestoreResult
	sn, err := c.Store.Snapshot(ctx, name, id)
	if err != nil {
		return res, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return res, err
	}
	if sn.Root != projectRoot(site) {
		return res, errcode.Errorf(errcode.Conflict, fmt.Sprintf("the snapshot is of %s, the site is now in %s", sn.Root, projectRoot(site)))
	}
	before, err := c.createSnapshot(ctx, name, SnapshotPreRestore, "before restoring "+sn.ID, sn.ID)
	if err != nil {
		return res, err
	}
	res.Restored, res.Before = sn, before
	bg := context.WithoutCancel(ctx)

	// Files: the current folder moves aside, the snapshot is cloned in, and
	// the folders snapshots leave out (.git, node_modules) move back.
	root := sn.Root
	aside := root + ".covo-restore-" + time.Now().Format("150405")
	if err := os.Rename(root, aside); err != nil {
		return res, fmt.Errorf("move %s aside: %w", root, err)
	}
	putBack := func() {
		_ = os.RemoveAll(root)
		_ = os.Rename(aside, root)
	}
	if err := cloneTree(sn.FilesDir, root); err != nil {
		putBack()
		return res, err
	}
	if err := moveSkipped(aside, root); err != nil {
		putBack()
		return res, err
	}

	// Database.
	if sn.DBFile != "" {
		if err := c.restoreSnapshotDB(ctx, site, sn.DBEngine, sn.DBName, sn.DBFile); err != nil {
			putBack()
			if before.DBFile != "" {
				_ = c.restoreSnapshotDB(bg, site, before.DBEngine, before.DBName, before.DBFile)
			}
			return res, err
		}
	}
	if err := os.RemoveAll(aside); err != nil {
		return res, fmt.Errorf("remove %s: %w", aside, err)
	}
	return res, nil
}

// restoreSnapshotDB recreates db and loads a snapshot's dump into it.
func (c *Core) restoreSnapshotDB(ctx context.Context, site store.Site, engine, db, file string) error {
	if engine == EnginePostgres {
		role := ""
		if site.Database == db {
			role = site.DBUser
		}
		owner := ""
		if role != "" {
			owner = " OWNER " + postgres.Ident(role)
		}
		if _, err := c.pgQuery(ctx, "postgres", "DROP DATABASE IF EXISTS "+postgres.Ident(db)+" WITH (FORCE);"); err != nil {
			return err
		}
		if _, err := c.pgQuery(ctx, "postgres", "CREATE DATABASE "+postgres.Ident(db)+owner+" ENCODING 'UTF8';"); err != nil {
			return err
		}
		dir, _, err := c.ensurePostgres(ctx)
		if err != nil {
			return err
		}
		_, port, pw, err := c.postgresRoot(ctx)
		if err != nil {
			return err
		}
		return postgres.Restore(ctx, filepath.Join(dir, "bin"), port, pw, db, role, file, c.Layout.Tmp)
	}
	return c.restoreDatabaseBackup(ctx, db, file)
}

func (c *Core) dumpMySQL(ctx context.Context, db, path string) error {
	client, port, pw, err := c.mysqlRoot(ctx)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	gz := gzip.NewWriter(f)
	err = mysql.Dump(ctx, filepath.Join(filepath.Dir(client), "mysqldump"),
		mysql.Server{Host: "127.0.0.1", Port: port, User: mysql.User, Password: pw}, db, gz)
	if cerr := gz.Close(); err == nil {
		err = cerr
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("dump %s: %w", db, err)
	}
	return nil
}

func (c *Core) dumpPostgres(ctx context.Context, db, path string) error {
	dir, _, err := c.ensurePostgres(ctx)
	if err != nil {
		return err
	}
	_, port, pw, err := c.postgresRoot(ctx)
	if err != nil {
		return err
	}
	return postgres.Dump(ctx, filepath.Join(dir, "bin"), port, pw, db, path, c.Layout.Tmp)
}

// cloneTree copies src to dst (which must not exist) with APFS clones, then
// removes the folders snapshots leave out. Without clonefile (another file
// system) it copies.
func cloneTree(src, dst string) error {
	if err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW); err != nil {
		if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EXDEV) {
			return fmt.Errorf("clone %s: %w", src, err)
		}
		if err := copyTree(src, dst); err != nil {
			return err
		}
	}
	var drop []string
	err := filepath.WalkDir(dst, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != dst && snapshotSkip[d.Name()] {
			drop = append(drop, p)
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", dst, err)
	}
	for _, p := range drop {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return nil
}

// moveSkipped moves the folders snapshots leave out from the old tree into
// the restored one, where their parent exists and they do not.
func moveSkipped(old, restored string) error {
	var moves [][2]string
	err := filepath.WalkDir(old, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && p != old && snapshotSkip[d.Name()] {
			rel, _ := filepath.Rel(old, p)
			moves = append(moves, [2]string{p, filepath.Join(restored, rel)})
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("walk %s: %w", old, err)
	}
	for _, m := range moves {
		if _, err := os.Stat(filepath.Dir(m[1])); err != nil {
			continue
		}
		if _, err := os.Lstat(m[1]); err == nil {
			continue
		}
		if err := os.Rename(m[0], m[1]); err != nil {
			return fmt.Errorf("move %s back: %w", m[0], err)
		}
	}
	return nil
}
