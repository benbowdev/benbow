// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/benbowdev/benbow/internal/version"
)

// SnapshotExport is what ExportSnapshot wrote.
type SnapshotExport struct {
	Path  string `json:"path"`
	Bytes int64  `json:"bytes"`
	Files int    `json:"files"`
	// Database is the dump's name in the zip ("" when the snapshot has
	// none).
	Database string   `json:"database,omitempty"`
	Warnings []string `json:"warnings"`
}

// snapshotManifest is benbow-snapshot.json in an exported zip.
type snapshotManifest struct {
	Benbow          string            `json:"benbow"`
	Site            string            `json:"site"`
	Snapshot        string            `json:"snapshot"`
	CreatedAt       time.Time         `json:"created_at"`
	Kind            string            `json:"kind"`
	Note            string            `json:"note,omitempty"`
	PHP             string            `json:"php,omitempty"`
	PHPSettings     map[string]string `json:"php_settings,omitempty"`
	DatabaseEngine  string            `json:"database_engine,omitempty"`
	DatabaseVersion string            `json:"database_version,omitempty"`
	Database        string            `json:"database,omitempty"`
}

// ExportSnapshot writes a snapshot (the newest when id is "") as a zip at
// out, an absolute path that must not exist yet; with out "" the zip goes
// to Benbow's tmp/exports for the caller to move (the app does, to where
// its save panel points: the daemon has no access to Desktop or Documents).
// The zip holds the project folder under <site>/, the database as
// <site>.sql (MySQL, plain SQL) or <site>.pgdump (pg_dump archive), and
// benbow-snapshot.json. `benbow import archive` reads it back, and so can
// any tool that takes a folder and a .sql file. The snapshot is cloned
// under the lock, then zipped without it, so other commands do not wait.
func (c *Core) ExportSnapshot(ctx context.Context, name, id, out string) (SnapshotExport, error) {
	if out == "" {
		dir := filepath.Join(c.Layout.Tmp, "exports")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return SnapshotExport{}, fmt.Errorf("create %s: %w", dir, err)
		}
		removeOlder(dir, time.Hour) // exports a caller never moved
		out = filepath.Join(dir, name+"-"+time.Now().Format("20060102-150405.000")+".zip")
	}
	res := SnapshotExport{Path: filepath.Clean(out), Warnings: []string{}}
	if !filepath.IsAbs(out) {
		return res, errcode.Errorf(errcode.InvalidInput, "the export path must be absolute")
	}
	if !strings.EqualFold(filepath.Ext(out), ".zip") {
		return res, errcode.Errorf(errcode.InvalidInput, "the export path must end in .zip")
	}
	if _, err := os.Lstat(res.Path); err == nil {
		return res, errcode.Newf(errcode.Conflict, "%s already exists", res.Path)
	}
	if fi, err := os.Stat(filepath.Dir(res.Path)); err != nil || !fi.IsDir() {
		return res, errcode.Newf(errcode.NotFound, "folder %s does not exist", filepath.Dir(res.Path))
	}

	tmp, err := os.MkdirTemp(c.Layout.Tmp, "export-")
	if err != nil {
		return res, fmt.Errorf("create temporary folder: %w", err)
	}
	defer os.RemoveAll(tmp)
	var sn store.Snapshot
	err = c.locked(ctx, func() error {
		var err error
		if id == "" {
			all, err := c.ListSnapshots(ctx, name)
			if err != nil {
				return err
			}
			if len(all) == 0 {
				return errcode.Newf(errcode.NotFound, "%s has no snapshots: take one first", name)
			}
			id = all[0].ID
		}
		if sn, err = c.Store.Snapshot(ctx, name, id); err != nil {
			return err
		}
		return cloneOrCopy(filepath.Dir(sn.FilesDir), filepath.Join(tmp, "snap"))
	})
	if err != nil {
		return res, err
	}
	copySn := sn
	copySn.FilesDir = filepath.Join(tmp, "snap", "files")
	if sn.DBFile != "" {
		copySn.DBFile = filepath.Join(tmp, "snap", filepath.Base(sn.DBFile))
	}
	if err := checkSnapshot(copySn); err != nil {
		return res, err
	}

	partial := res.Path + ".partial"
	f, err := os.OpenFile(partial, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return res, fmt.Errorf("create %s: %w", partial, err)
	}
	done := false
	defer func() {
		if !done {
			_ = f.Close()
			_ = os.Remove(partial)
		}
	}()
	zw := zip.NewWriter(f)
	if res.Files, err = zipTree(ctx, zw, copySn.FilesDir, name); err != nil {
		return res, err
	}
	man := snapshotManifest{
		Benbow: version.Version, Site: name, Snapshot: sn.ID, CreatedAt: sn.CreatedAt.UTC(), Kind: sn.Kind, Note: sn.Note,
		PHP: sn.PHPMinor, PHPSettings: sn.PHPSettings, DatabaseEngine: sn.DBEngine, DatabaseVersion: sn.DBVersion,
	}
	if copySn.DBFile != "" {
		if sn.DBEngine == EnginePostgres {
			res.Database = name + ".pgdump"
			res.Warnings = append(res.Warnings, res.Database+" is a pg_dump archive: load it with pg_restore (benbow import archive loads MySQL dumps only)")
		} else {
			res.Database = name + ".sql"
		}
		if err := zipDump(zw, copySn.DBFile, res.Database); err != nil {
			return res, err
		}
		man.Database = res.Database
	}
	if err := zipJSON(zw, "benbow-snapshot.json", man); err != nil {
		return res, err
	}
	if err := zw.Close(); err != nil {
		return res, fmt.Errorf("write %s: %w", partial, err)
	}
	if err := f.Close(); err != nil {
		return res, fmt.Errorf("write %s: %w", partial, err)
	}
	if err := os.Rename(partial, res.Path); err != nil {
		_ = os.Remove(partial)
		return res, fmt.Errorf("move %s into place: %w", res.Path, err)
	}
	done = true
	if fi, err := os.Stat(res.Path); err == nil {
		res.Bytes = fi.Size()
	}
	return res, nil
}

// removeOlder deletes the files in dir last changed more than age ago.
func removeOlder(dir string, age time.Duration) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && time.Since(fi.ModTime()) > age {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// cloneOrCopy clones a folder (APFS), copying it on other volumes.
func cloneOrCopy(src, dst string) error {
	err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EXDEV) {
		return fmt.Errorf("clone %s: %w", src, err)
	}
	return copyTree(src, dst)
}

// storedExt are files already compressed: deflating them again only
// costs time.
var storedExt = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".gif": true, ".webp": true, ".avif": true, ".heic": true,
	".zip": true, ".gz": true, ".tgz": true, ".bz2": true, ".xz": true, ".7z": true, ".rar": true, ".zst": true,
	".mp4": true, ".mov": true, ".m4v": true, ".webm": true, ".mp3": true, ".m4a": true, ".ogg": true,
	".woff": true, ".woff2": true,
}

// zipTree adds a folder's files and symlinks under prefix/.
func zipTree(ctx context.Context, zw *zip.Writer, dir, prefix string) (int, error) {
	n := 0
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil {
			err = ctx.Err()
		}
		if err == nil && p != dir {
			var added bool
			added, err = addZipEntry(zw, dir, prefix, p, d)
			if added {
				n++
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		return nil
	})
	if err != nil {
		return n, fmt.Errorf("zip %s: %w", dir, err)
	}
	return n, nil
}

// addZipEntry adds one folder, file or symlink; it reports whether it was a
// file or symlink.
func addZipEntry(zw *zip.Writer, dir, prefix, p string, d fs.DirEntry) (bool, error) {
	rel, err := filepath.Rel(dir, p)
	if err != nil {
		return false, fmt.Errorf("relative path: %w", err)
	}
	info, err := d.Info()
	if err != nil {
		return false, fmt.Errorf("stat: %w", err)
	}
	mode := info.Mode()
	if !d.IsDir() && !mode.IsRegular() && mode&fs.ModeSymlink == 0 {
		return false, nil // sockets, pipes
	}
	h, err := zip.FileInfoHeader(info)
	if err != nil {
		return false, fmt.Errorf("zip header: %w", err)
	}
	h.Name = prefix + "/" + filepath.ToSlash(rel)
	var body io.Reader
	switch {
	case d.IsDir():
		h.Name += "/"
	case mode&fs.ModeSymlink != 0:
		target, err := os.Readlink(p)
		if err != nil {
			return false, fmt.Errorf("read link: %w", err)
		}
		body = strings.NewReader(target)
	default:
		h.Method = zip.Deflate
		if storedExt[strings.ToLower(filepath.Ext(p))] {
			h.Method = zip.Store
		}
		f, err := os.Open(p)
		if err != nil {
			return false, fmt.Errorf("open: %w", err)
		}
		defer f.Close()
		body = f
	}
	w, err := zw.CreateHeader(h)
	if err != nil {
		return false, fmt.Errorf("zip entry: %w", err)
	}
	if body == nil {
		return false, nil
	}
	if _, err := io.Copy(w, body); err != nil {
		return false, fmt.Errorf("write: %w", err)
	}
	return true, nil
}

// zipDump adds the snapshot's dump, unpacked when it is gzip.
func zipDump(zw *zip.Writer, file, name string) error {
	in, err := os.Open(file)
	if err != nil {
		return fmt.Errorf("read %s: %w", file, err)
	}
	defer in.Close()
	var r io.Reader = in
	method := zip.Store // a pg_dump archive is compressed already
	if strings.HasSuffix(file, ".gz") {
		gz, err := gzip.NewReader(in)
		if err != nil {
			return fmt.Errorf("read %s: %w", file, err)
		}
		defer gz.Close()
		r, method = gz, zip.Deflate
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: time.Now()})
	if err != nil {
		return fmt.Errorf("zip %s: %w", name, err)
	}
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("zip %s: %w", file, err)
	}
	return nil
}

func zipJSON(zw *zip.Writer, name string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", name, err)
	}
	w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return fmt.Errorf("zip %s: %w", name, err)
	}
	if _, err := w.Write(append(b, '\n')); err != nil {
		return fmt.Errorf("zip %s: %w", name, err)
	}
	return nil
}
