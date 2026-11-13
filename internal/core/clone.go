// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"

	"github.com/benbowdev/benbow/internal/dotenv"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/benbowdev/benbow/internal/wpconfig"
)

// CloneRequest is the input of CloneSite.
type CloneRequest struct {
	Source string
	Name   string
	// PHP defaults to the source's version.
	PHP string
	// Folder is the new project folder (default: next to the source's,
	// named after the new site). It must not exist.
	Folder string
	// SameFolder serves the source's folder under the new name instead of
	// copying anything (e.g. to try another PHP version): no database, no
	// files.
	SameFolder bool
}

// CloneResult is the new site and what the clone did.
type CloneResult struct {
	SiteResult
	// Folder is the new project folder ("" with SameFolder).
	Folder string `json:"folder,omitempty"`
	// Database is the new database ("" when the source has none).
	Database string `json:"database,omitempty"`
	// URLFrom is the address replaced in the database (WordPress).
	URLFrom string `json:"url_from,omitempty"`
	// Warnings are things to check by hand.
	Warnings []string `json:"warnings,omitempty"`
}

// CloneSite copies a site under a new name: the project folder with APFS
// clones (everything, .git and node_modules included), its database into a
// new one with its own user, the project's configuration pointed at them,
// and WordPress's address replaced. If any step fails, everything is undone.
func (c *Core) CloneSite(ctx context.Context, req CloneRequest) (CloneResult, error) {
	src, err := c.Store.Site(ctx, req.Source)
	if err != nil {
		return CloneResult{}, err
	}
	if src.IsApp() {
		return CloneResult{}, errcode.Errorf(errcode.Unsupported, "apps cannot be cloned yet: copy the folder and add it as a new site")
	}
	if req.PHP == "" {
		req.PHP = src.PHPMinor
	}
	if req.SameFolder {
		res, err := c.AddSite(ctx, AddSiteRequest{
			Name: req.Name, PHP: req.PHP, Path: src.Path, Force: true, Group: src.GroupID,
			PHPSettings: maps.Clone(src.PHPSettings),
		})
		return CloneResult{SiteResult: res}, err
	}

	root := projectRoot(src)
	if req.Folder == "" {
		req.Folder = filepath.Join(filepath.Dir(root), req.Name)
	}
	req.Folder = filepath.Clean(req.Folder)
	if !filepath.IsAbs(req.Folder) {
		return CloneResult{}, errcode.Errorf(errcode.InvalidInput, "the new folder must be an absolute path")
	}
	if err := paths.CheckSafe(req.Folder); err != nil {
		return CloneResult{}, err
	}
	if _, err := os.Lstat(req.Folder); err == nil {
		return CloneResult{}, errcode.Newf(errcode.Conflict, "%s already exists: choose a new folder", req.Folder)
	}
	rel, err := filepath.Rel(root, src.Path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return CloneResult{}, errcode.Errorf(errcode.Unsupported, "the site's document root is outside its project folder")
	}
	// Check the name before copying gigabytes.
	if _, err := c.Store.Site(ctx, req.Name); err == nil {
		return CloneResult{}, errcode.Newf(errcode.Conflict, "site %q already exists", req.Name)
	}
	if err := c.checkSiteFields(AddSiteRequest{Name: req.Name, Domain: req.Name + "." + suffixLocalhost, PHP: req.PHP, Path: req.Folder}); err != nil {
		return CloneResult{}, err
	}
	var cfg cloneConfig
	if src.Database != "" {
		if cfg, err = findCloneConfig(src, root); err != nil {
			return CloneResult{}, err
		}
	}

	if err := cloneFolder(root, req.Folder); err != nil {
		_ = os.RemoveAll(req.Folder)
		return CloneResult{}, err
	}
	bg := context.WithoutCancel(ctx)
	siteAdded := false
	fail := func(err error) (CloneResult, error) {
		if siteAdded {
			_, _ = c.RemoveSiteAndDatabase(bg, req.Name)
		}
		_ = os.RemoveAll(req.Folder)
		return CloneResult{}, err
	}

	add, err := c.AddSite(ctx, AddSiteRequest{
		Name: req.Name, PHP: req.PHP, Path: filepath.Join(req.Folder, rel), Force: true, Group: src.GroupID,
		PHPSettings: maps.Clone(src.PHPSettings),
	})
	if err != nil {
		return fail(err)
	}
	siteAdded = true
	res := CloneResult{SiteResult: add, Folder: req.Folder}
	if src.Database == "" {
		return res, nil
	}

	// The database: a new one with its own user, loaded from a dump of the
	// source's.
	engine := engineOf(src)
	db, err := c.CreateSiteDatabaseOn(ctx, req.Name, "", engine)
	if err != nil {
		return fail(err)
	}
	res.Database = db.Database
	tmp, err := os.MkdirTemp(c.Layout.Tmp, "clone-")
	if err != nil {
		return fail(fmt.Errorf("create temporary folder: %w", err))
	}
	defer os.RemoveAll(tmp)
	dump := filepath.Join(tmp, "database.sql.gz")
	if engine == EnginePostgres {
		dump = filepath.Join(tmp, "database.dump")
		err = c.dumpPostgres(ctx, src.Database, dump)
	} else {
		err = c.dumpMySQL(ctx, src.Database, dump)
	}
	if err != nil {
		return fail(err)
	}
	site, err := c.Store.Site(ctx, req.Name)
	if err != nil {
		return fail(err)
	}
	if err := c.restoreSnapshotDB(ctx, site, engine, db.Database, dump); err != nil {
		return fail(err)
	}

	// The configuration, in the new folder only.
	cfg.path = filepath.Join(req.Folder, cfg.rel)
	switch cfg.framework {
	case FrameworkLaravel:
		err = editEnvFile(cfg.path, []dotenv.Pair{
			{Key: "APP_URL", Value: add.URL},
			{Key: "DB_DATABASE", Value: db.Database}, {Key: "DB_USERNAME", Value: db.User}, {Key: "DB_PASSWORD", Value: db.Password},
		})
	case FrameworkWordPress:
		home, _ := c.wpHome(ctx, db.Database, cfg.prefix)
		res.URLFrom = home
		var w []string
		if w, err = pointWPConfig(cfg.path, db, home, add.URL); err == nil {
			res.Warnings = append(res.Warnings, w...)
			if home != "" && home != add.URL {
				err = c.replaceURL(ctx, site, home, add.URL)
			}
		}
	default:
		res.Warnings = append(res.Warnings, fmt.Sprintf("Benbow found no wp-config.php or .env: point the project at database %s (user %s) by hand", db.Database, db.User))
	}
	if err != nil {
		return fail(err)
	}
	if res.Site, err = c.Store.Site(ctx, req.Name); err != nil {
		return res, err
	}
	return res, nil
}

// cloneConfig is the project file a clone points at its new database.
type cloneConfig struct {
	framework string
	rel       string // relative to the project folder
	path      string // in the new folder
	prefix    string // WordPress table prefix
}

// findCloneConfig finds wp-config.php or .env inside the project folder.
// A wp-config.php above the folder (which WordPress allows) is refused: the
// clone would share it with the source.
func findCloneConfig(src store.Site, root string) (cloneConfig, error) {
	if src.Framework == FrameworkLaravel {
		if _, err := os.Stat(filepath.Join(root, ".env")); err == nil {
			return cloneConfig{framework: FrameworkLaravel, rel: ".env"}, nil
		}
		return cloneConfig{}, nil
	}
	p := filepath.Join(src.Path, "wp-config.php")
	if b, err := os.ReadFile(p); err == nil {
		rel, _ := filepath.Rel(root, p)
		return cloneConfig{framework: FrameworkWordPress, rel: rel, prefix: wpconfig.Read(string(b)).TablePrefix}, nil
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(src.Path), "wp-config.php")); err == nil && src.Path == root {
		return cloneConfig{}, errcode.Errorf(errcode.Unsupported, "wp-config.php sits above the site's folder, so a clone would share it: move it into the folder first")
	}
	return cloneConfig{}, nil
}

// pointWPConfig sets the database and, when wp-config.php defines them, the
// address constants of a cloned WordPress.
func pointWPConfig(path string, db SiteDatabase, dbHome, url string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	values, warnings := wpAddressDefines(string(b), dbHome, url)
	values["DB_NAME"], values["DB_USER"], values["DB_PASSWORD"] = db.Database, db.User, db.Password
	out, err := wpconfig.Set(string(b), values)
	if err != nil {
		return nil, err
	}
	return warnings, rewriteProjectFile(path, []byte(out))
}

// cloneFolder copies src to dst (which must not exist) with one APFS clone,
// everything included. Without clonefile (another file system) it copies.
func cloneFolder(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	err := unix.Clonefile(src, dst, unix.CLONE_NOFOLLOW)
	if err == nil {
		return nil
	}
	if !errors.Is(err, unix.ENOTSUP) && !errors.Is(err, unix.EXDEV) {
		if errors.Is(err, fs.ErrExist) {
			return errcode.Newf(errcode.Conflict, "%s already exists: choose a new folder", dst)
		}
		return fmt.Errorf("clone %s: %w", src, err)
	}
	return copyTree(src, dst)
}
