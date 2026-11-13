// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/mysql"
	"github.com/benbowdev/benbow/internal/postgres"
	"github.com/benbowdev/benbow/internal/store"
)

// Snapshots save a site's project folder and database under the Benbow
// folder. Files are cloned with APFS clonefile(2): a snapshot takes no
// space until the files change. .git, node_modules and generated folders
// are left out (snapshotSkip): a restore never rewinds git history or
// installed packages.

// Snapshot kinds.
const (
	SnapshotManual     = "manual"
	SnapshotPreRestore = "pre-restore"
	SnapshotPrePull    = "pre-pull"
	SnapshotMCP        = "mcp"
	// SnapshotSession is an agent session's starting point, kept while the
	// session is open; SnapshotCheckpoint is taken inside a session before
	// a risky change.
	SnapshotSession    = "session"
	SnapshotCheckpoint = "checkpoint"
)

const (
	settingSnapshotKeep = "snapshot_keep"
	defaultSnapshotKeep = 10
)

// snapshotSkip are folder names never saved nor restored, at any depth:
// git history, installed packages and what dev servers generate (they are
// rebuilt, can be large, and change all the time).
var snapshotSkip = map[string]bool{
	".git": true, "node_modules": true,
	".venv": true, "venv": true, "__pycache__": true, ".pytest_cache": true, ".mypy_cache": true, ".ruff_cache": true,
	".next": true, ".nuxt": true, ".svelte-kit": true, ".astro": true, ".turbo": true, ".parcel-cache": true,
}

// snapshotSkipText names them in messages.
const snapshotSkipText = ".git, node_modules and generated folders (.venv, .next, __pycache__…)"

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
	var sn store.Snapshot
	err := c.locked(ctx, func() error {
		var err error
		sn, err = c.createSnapshot(ctx, name, kind, note, "")
		return err
	})
	return sn, err
}

// createSnapshot never prunes the snapshot keep (the one being restored).
func (c *Core) createSnapshot(ctx context.Context, name, kind, note, keep string) (store.Snapshot, error) {
	return c.snapshot(ctx, snapOpts{site: name, kind: kind, note: note, spare: keep})
}

type snapOpts struct {
	site, kind, note string
	// spare is never pruned (the snapshot being restored).
	spare string
	// session links the snapshot to an agent session.
	session string
	// filesOnly leaves the database for later (saveSnapshotDB).
	filesOnly bool
}

func (c *Core) snapshot(ctx context.Context, o snapOpts) (store.Snapshot, error) {
	name, kind := o.site, o.kind
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return store.Snapshot{}, err
	}
	if kind == "" {
		kind = SnapshotManual
	}
	now := time.Now()
	sn := store.Snapshot{
		ID: now.Format("20060102-150405.000"), Site: name, CreatedAt: now, Kind: kind, Note: o.note, Root: projectRoot(site),
		SessionID: o.session,
	}
	if !site.IsApp() {
		sn.PHPMinor, sn.PHPSettings = site.PHPMinor, maps.Clone(site.PHPSettings)
	}
	if _, err := c.Store.Snapshot(ctx, name, sn.ID); err == nil {
		time.Sleep(time.Millisecond)
		return c.snapshot(ctx, o)
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
	if site.Database != "" && !o.filesOnly {
		if err := c.dumpSnapshotDB(ctx, site, &sn); err != nil {
			return fail(err)
		}
	}
	if err := c.Store.AddSnapshot(ctx, sn); err != nil {
		return fail(err)
	}
	c.pruneSnapshots(ctx, name, o.spare)
	return sn, nil
}

// dumpSnapshotDB writes the site's database into the snapshot's folder.
func (c *Core) dumpSnapshotDB(ctx context.Context, site store.Site, sn *store.Snapshot) error {
	dir := filepath.Dir(sn.FilesDir)
	sn.DBEngine, sn.DBName = engineOf(site), site.Database
	var err error
	if sn.DBEngine == EnginePostgres {
		sn.DBFile = filepath.Join(dir, "database.pgdump")
		err = c.dumpPostgres(ctx, site.Database, sn.DBFile)
	} else {
		sn.DBFile = filepath.Join(dir, "database.sql.gz")
		err = c.dumpMySQLOf(ctx, mysqlFor(sn.DBEngine), site.Database, sn.DBFile)
	}
	if err != nil {
		_ = os.Remove(sn.DBFile)
		sn.DBEngine, sn.DBName, sn.DBFile = "", "", ""
		return err
	}
	if fi, err := os.Stat(sn.DBFile); err == nil {
		sn.Bytes = fi.Size()
	}
	return nil
}

// pruneSnapshots deletes the oldest snapshots beyond the limit. The
// snapshots of an open agent session (its starting point and checkpoints)
// are not counted and never pruned here.
func (c *Core) pruneSnapshots(ctx context.Context, name, spare string) {
	keep, _ := c.SnapshotKeep(ctx)
	all, err := c.Store.ListSnapshots(ctx, name)
	if err != nil {
		return
	}
	open := ""
	if s, err := c.Store.OpenAgentSession(ctx, name); err == nil {
		open = s.ID
	}
	all = slices.DeleteFunc(all, func(sn store.Snapshot) bool { return open != "" && sn.SessionID == open })
	for _, sn := range all[min(len(all), keep):] {
		if sn.ID != spare {
			_ = c.removeSnapshot(ctx, sn)
		}
	}
}

func (c *Core) removeSnapshot(ctx context.Context, sn store.Snapshot) error {
	forgetUsage(filepath.Dir(sn.FilesDir))
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
	return c.locked(ctx, func() error {
		sn, err := c.Store.Snapshot(ctx, name, id)
		if err != nil {
			return err
		}
		return c.removeSnapshot(ctx, sn)
	})
}

// RestoreResult is a restore and the snapshot taken just before it.
type RestoreResult struct {
	Restored store.Snapshot `json:"restored"`
	// Before is the state just before the restore: restoring it undoes it.
	Before store.Snapshot `json:"before"`
	// PHPRestored is set when the site's PHP version and php.ini values
	// were brought back too; PHPDiffers when they differ from the
	// snapshot's but were left as they are.
	PHPRestored bool `json:"php_restored,omitempty"`
	PHPDiffers  bool `json:"php_differs,omitempty"`
}

// RestoreOptions are the choices of a restore.
type RestoreOptions struct {
	// PHP also brings back the site's PHP version and php.ini values saved
	// in the snapshot.
	PHP bool
}

// snapshotPHPDiffers reports whether a site's PHP differs from what a
// snapshot saved (false for snapshots without PHP).
func snapshotPHPDiffers(sn store.Snapshot, site store.Site) bool {
	if sn.PHPMinor == "" || site.IsApp() {
		return false
	}
	return sn.PHPMinor != site.PHPMinor || !maps.Equal(sn.PHPSettings, site.PHPSettings)
}

// RestoreSnapshot puts a site's files and database back as they were in a
// snapshot. The current state is saved first (a pre-restore snapshot), and
// if any step fails it is put back.
func (c *Core) RestoreSnapshot(ctx context.Context, name, id string, opts RestoreOptions) (RestoreResult, error) {
	var res RestoreResult
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.restoreSnapshot(ctx, name, id)
		return err
	})
	if err != nil {
		return res, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return res, err
	}
	if !snapshotPHPDiffers(res.Restored, site) {
		return res, nil
	}
	if !opts.PHP {
		res.PHPDiffers = true
		return res, nil
	}
	if res.Restored.PHPMinor != site.PHPMinor {
		if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: name, PHP: res.Restored.PHPMinor}); err != nil {
			return res, fmt.Errorf("files and database restored, but not PHP %s: %w", res.Restored.PHPMinor, err)
		}
	}
	if !maps.Equal(res.Restored.PHPSettings, site.PHPSettings) {
		values := map[string]string{}
		for k := range site.PHPSettings {
			values[k] = "" // cleared unless the snapshot has it
		}
		maps.Copy(values, res.Restored.PHPSettings)
		if _, err := c.SetSitePHPSettings(ctx, name, values); err != nil {
			return res, fmt.Errorf("files, database and PHP version restored, but not the php.ini values: %w", err)
		}
	}
	res.PHPRestored = true
	return res, nil
}

// restoreSnapshot is RestoreSnapshot; the caller holds the lock.
func (c *Core) restoreSnapshot(ctx context.Context, name, id string) (RestoreResult, error) {
	var res RestoreResult
	if _, err := c.recoverRestores(ctx); err != nil {
		return res, err
	}
	sn, err := c.Store.Snapshot(ctx, name, id)
	if err != nil {
		return res, err
	}
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return res, err
	}
	if sn.Root != projectRoot(site) {
		return res, errcode.Newf(errcode.Conflict, "the snapshot is of %s, the site is now in %s", sn.Root, projectRoot(site))
	}
	before, err := c.createSnapshot(ctx, name, SnapshotPreRestore, "before restoring "+sn.ID, sn.ID)
	if err != nil {
		return res, err
	}
	res.Restored, res.Before = sn, before
	bg := context.WithoutCancel(ctx)

	// Files: the current folder moves aside, the snapshot is cloned in, and
	// the folders snapshots leave out (snapshotSkip) move back.
	root := sn.Root
	aside := root + ".benbow-restore-" + time.Now().Format("150405")
	// The journal lets a restore cut short (crash, kill, reboot) be put
	// back by the next restore or daemon start; every return below leaves
	// a whole state behind, so it goes when this function returns.
	j := restoreJournal{Site: name, Root: root, Aside: aside, DBEngine: before.DBEngine, DBName: before.DBName, DBFile: before.DBFile}
	if err := c.writeRestoreJournal(j); err != nil {
		return res, err
	}
	defer func() { _ = os.Remove(c.restoreJournalPath(name)) }()
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
	return c.restoreDatabaseBackupOf(ctx, mysqlFor(engine), db, file)
}

func (c *Core) dumpMySQLOf(ctx context.Context, s mysqlServer, db, path string) error {
	client, port, pw, err := c.mysqlRootOf(ctx, s)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	gz := gzip.NewWriter(f)
	err = mysql.Dump(ctx, filepath.Join(filepath.Dir(client), "mysqldump"),
		mysql.Server{Host: "127.0.0.1", Port: port, User: mysql.User, Password: pw, Client57: s.legacy}, db, gz)
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

// restoreJournal is a restore in progress: the folder moved aside and the
// database dump taken just before it.
type restoreJournal struct {
	Site     string `json:"site"`
	Root     string `json:"root"`
	Aside    string `json:"aside"`
	DBEngine string `json:"db_engine,omitempty"`
	DBName   string `json:"db_name,omitempty"`
	DBFile   string `json:"db_file,omitempty"`
	// FilesBack is set once the folder is back, when only the database
	// is left to put back.
	FilesBack bool `json:"files_back,omitempty"`
}

func (c *Core) restoreJournalDir() string { return filepath.Join(c.Layout.Data, "restores") }

func (c *Core) restoreJournalPath(site string) string {
	return filepath.Join(c.restoreJournalDir(), site+".json")
}

func (c *Core) writeRestoreJournal(j restoreJournal) error {
	if err := os.MkdirAll(c.restoreJournalDir(), 0o700); err != nil {
		return fmt.Errorf("create %s: %w", c.restoreJournalDir(), err)
	}
	b, err := json.Marshal(j)
	if err != nil {
		return fmt.Errorf("encode restore journal: %w", err)
	}
	_, err = fsutil.WriteAtomic(c.restoreJournalPath(j.Site), b, 0o600)
	return err
}

// RecoverRestores puts back the sites whose restore was cut short (the
// daemon runs it when it starts) and returns their names.
func (c *Core) RecoverRestores(ctx context.Context) ([]string, error) {
	var sites []string
	err := c.locked(ctx, func() error {
		var err error
		sites, err = c.recoverRestores(ctx)
		return err
	})
	return sites, err
}

// recoverRestores is RecoverRestores; the caller holds the lock. A site
// goes back to how it was before the restore started: the folder moved
// aside comes back and the database is reloaded from the dump taken first.
func (c *Core) recoverRestores(ctx context.Context) ([]string, error) {
	entries, err := os.ReadDir(c.restoreJournalDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", c.restoreJournalDir(), err)
	}
	var sites []string
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		path := filepath.Join(c.restoreJournalDir(), e.Name())
		b, err := os.ReadFile(path)
		if err != nil {
			return sites, fmt.Errorf("read %s: %w", path, err)
		}
		var j restoreJournal
		if err := json.Unmarshal(b, &j); err != nil || j.Site == "" || j.Root == "" || j.Aside == "" {
			return sites, errcode.Newf(errcode.Internal, "unreadable restore journal %s: move it away and check the site by hand", path)
		}
		if !j.FilesBack {
			if _, err := os.Lstat(j.Aside); err != nil {
				// Nothing was moved yet, or the restore had finished.
				_ = os.Remove(path)
				continue
			}
			if err := os.RemoveAll(j.Root); err != nil {
				return sites, fmt.Errorf("remove the half-restored %s: %w", j.Root, err)
			}
			if err := os.Rename(j.Aside, j.Root); err != nil {
				return sites, fmt.Errorf("put %s back: %w", j.Root, err)
			}
			j.FilesBack = true
			if err := c.writeRestoreJournal(j); err != nil {
				return sites, err
			}
		}
		if j.DBFile != "" {
			site, err := c.Store.Site(ctx, j.Site)
			if err != nil {
				return sites, err
			}
			if err := c.restoreSnapshotDB(ctx, site, j.DBEngine, j.DBName, j.DBFile); err != nil {
				return sites, fmt.Errorf("put the database of %s back: %w", j.Site, err)
			}
		}
		if err := os.Remove(path); err != nil {
			return sites, fmt.Errorf("remove %s: %w", path, err)
		}
		sites = append(sites, j.Site)
	}
	return sites, nil
}
