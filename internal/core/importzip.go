// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/benbowdev/benbow/internal/dotenv"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/benbowdev/benbow/internal/wpconfig"
)

// Site archives: a zip or tar.gz with a site's folder somewhere inside and a
// .sql (or .sql.gz) dump anywhere: Local's exports, cPanel and host
// backups, Duplicator archives, hand-made zips.

const (
	maxArchiveFiles = 1_000_000
	maxArchiveTotal = 100 << 30 // unpacked
)

// ArchiveImportRequest is the input of ImportArchive.
type ArchiveImportRequest struct {
	File string
	// Name defaults to one made from the archive's file name.
	Name string
	// PHP defaults to a version suited to the WordPress inside, else 8.4.
	PHP string
	// Folder is the new site's folder (default ~/Sites/<name>); it must be
	// missing or empty.
	Folder string
	// DryRun only reads the archive's list of files.
	DryRun bool
}

// ArchiveImportResult is what ImportArchive found and did.
type ArchiveImportResult struct {
	// Site is the new site (not set on a dry run).
	Site *SiteResult `json:"site,omitempty"`
	Name string      `json:"name"`
	// Root is the site's folder inside the archive ("" for the top).
	Root      string `json:"root"`
	Folder    string `json:"folder"`
	Framework string `json:"framework,omitempty"`
	// WordPress is the version found in wp-includes/version.php.
	WordPress string `json:"wordpress,omitempty"`
	PHP       string `json:"php"`
	PHPReason string `json:"php_reason,omitempty"`
	// SQL is the dump loaded, as named in the archive ("" when none).
	SQL      string   `json:"sql,omitempty"`
	Database string   `json:"database,omitempty"`
	URLFrom  string   `json:"url_from,omitempty"`
	Warnings []string `json:"warnings"`
	DryRun   bool     `json:"dry_run"`
}

// archiveEntry is a regular file in an archive.
type archiveEntry struct {
	name string // cleaned, slash-separated
	size int64
}

// ImportArchive creates a site from an archive: the site's folder goes to
// Folder, the dump into a new database with its own user, wp-config.php or
// .env is pointed at it, and WordPress's address is replaced. The archive is
// only read. If any step fails, everything is undone.
func (c *Core) ImportArchive(ctx context.Context, req ArchiveImportRequest) (ArchiveImportResult, error) {
	res := ArchiveImportResult{DryRun: req.DryRun, Warnings: []string{}}
	kind, err := archiveKind(req.File)
	if err != nil {
		return res, err
	}
	entries, err := listArchive(req.File, kind)
	if err != nil {
		return res, err
	}
	plan, err := planArchive(entries)
	if err != nil {
		return res, err
	}
	res.Root, res.SQL, res.Framework = plan.root, plan.sql, plan.framework
	res.Warnings = append(res.Warnings, plan.warnings...)
	if req.Name == "" {
		req.Name = nameFromArchive(req.File)
	}
	res.Name = req.Name
	if _, err := c.Store.Site(ctx, req.Name); err == nil {
		return res, errcode.Newf(errcode.Conflict, "site %q already exists: choose another name", req.Name)
	}
	if req.Folder == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return res, fmt.Errorf("resolve home directory: %w", err)
		}
		req.Folder = filepath.Join(home, "Sites", req.Name)
	}
	res.Folder = filepath.Clean(req.Folder)
	if err := paths.CheckSafe(res.Folder); err != nil {
		return res, err
	}
	if !emptyOrMissing(res.Folder) {
		return res, errcode.Newf(errcode.Conflict, "%s is not empty: choose a new folder", res.Folder)
	}
	res.PHP, res.PHPReason = req.PHP, ""
	if req.DryRun {
		if plan.framework == FrameworkWordPress {
			res.WordPress = archiveWordPressVersion(req.File, kind, path.Join(plan.root, "wp-includes/version.php"))
		}
		if res.PHP == "" {
			res.PHP, res.PHPReason = phpForArchive(res.WordPress)
		}
		return res, nil
	}

	// Unpack into Benbow's temporary folder, then move the site's folder.
	tmp, err := os.MkdirTemp(c.Layout.Tmp, "archive-")
	if err != nil {
		return res, fmt.Errorf("create temporary folder: %w", err)
	}
	defer os.RemoveAll(tmp)
	if err := extractArchive(req.File, kind, tmp); err != nil {
		return res, err
	}
	src := filepath.Join(tmp, filepath.FromSlash(plan.root))
	var dump string
	if plan.sql != "" {
		// Out of the site's folder first: a dump inside the web root could
		// be downloaded by anyone who can reach the site.
		dump = filepath.Join(tmp, "benbow-dump"+sqlExt(plan.sql))
		if err := os.Rename(filepath.Join(tmp, filepath.FromSlash(plan.sql)), dump); err != nil {
			return res, fmt.Errorf("move the dump aside: %w", err)
		}
		if within(filepath.Join(tmp, filepath.FromSlash(plan.sql)), src) {
			res.Warnings = append(res.Warnings, plan.sql+" was left out of the site's folder: anyone reaching the site could download it")
		}
	}
	if res.WordPress = wordPressVersion(src); res.WordPress != "" {
		res.Framework = FrameworkWordPress
	}
	if res.PHP == "" {
		res.PHP, res.PHPReason = phpForArchive(res.WordPress)
	}
	created := !fileExists(res.Folder)
	if err := moveTree(src, res.Folder); err != nil {
		return res, err
	}
	bg := context.WithoutCancel(ctx)
	siteAdded := false
	fail := func(err error) (ArchiveImportResult, error) {
		if siteAdded {
			_, _ = c.RemoveSiteAndDatabase(bg, req.Name)
		}
		cleanupDir(res.Folder, created)
		return res, err
	}

	add, err := c.AddSite(ctx, AddSiteRequest{Name: req.Name, PHP: res.PHP, Path: res.Folder, Force: true, NewDatabase: dump != ""})
	if err != nil {
		return fail(err)
	}
	siteAdded = true
	res.Site = &add
	if dump == "" {
		if res.Framework != "" {
			res.Warnings = append(res.Warnings, "the archive has no .sql dump: the site has no database")
		}
		return res, nil
	}
	site, err := c.Store.Site(ctx, req.Name)
	if err != nil {
		return fail(err)
	}
	db, err := c.siteDatabase(ctx, site)
	if err != nil {
		return fail(err)
	}
	res.Database = db.Database
	client, port, rootPW, err := c.mysqlRoot(ctx)
	if err != nil {
		return fail(err)
	}
	if err := c.loadIntoDatabase(ctx, projectDB{}, db.Database, dump, client, port, rootPW); err != nil {
		return fail(err)
	}
	host := "127.0.0.1:" + strconv.Itoa(port)
	root := projectRoot(site)
	switch {
	case site.Framework == FrameworkLaravel:
		w, err := prepareEnv(root, db, host, add.URL)
		if err != nil {
			return fail(err)
		}
		res.Warnings = append(res.Warnings, w...)
	case res.Framework == FrameworkWordPress:
		w, err := c.pointImportedWordPress(ctx, site, db, host, add.URL, &res)
		if err != nil {
			return fail(err)
		}
		res.Warnings = append(res.Warnings, w...)
	default:
		res.Warnings = append(res.Warnings, fmt.Sprintf("point the site's configuration at database %s (user %s, host %s); the password is in `benbow db show %s`", db.Database, db.User, host, req.Name))
	}
	if site, err = c.Store.Site(ctx, req.Name); err == nil {
		add.Site = site
		res.Site = &add
	}
	return res, nil
}

// pointImportedWordPress writes the database settings into wp-config.php
// (made from wp-config-sample.php when the archive has none) and replaces
// the old address in the database.
func (c *Core) pointImportedWordPress(ctx context.Context, site store.Site, db SiteDatabase, host, url string, res *ArchiveImportResult) ([]string, error) {
	var warnings []string
	cfg := filepath.Join(site.Path, "wp-config.php")
	if !fileExists(cfg) {
		sample := filepath.Join(site.Path, "wp-config-sample.php")
		if !fileExists(sample) {
			return []string{"no wp-config.php in the archive: create one for database " + db.Database}, nil
		}
		if err := copyFile(sample, cfg); err != nil {
			return nil, err
		}
		warnings = append(warnings, "the archive had no wp-config.php: Benbow made one from wp-config-sample.php (set your own salts if the site needs them)")
	}
	b, err := os.ReadFile(cfg)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", cfg, err)
	}
	prefix := wpconfig.Read(string(b)).TablePrefix
	old, _ := c.wpHome(ctx, db.Database, prefix)
	w, err := c.pointConfigAt(projectDB{framework: FrameworkWordPress, configPath: cfg}, db, host, url, old)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, w...)
	if old != "" && old != url {
		res.URLFrom = old
		if err := c.replaceURL(ctx, site, old, url); err != nil {
			return nil, err
		}
	} else if old == "" {
		warnings = append(warnings, "WordPress's address was not found in the dump (table prefix "+prefix+"): check the site's URL")
	}
	return warnings, nil
}

// prepareEnv points a Laravel project's .env (made from .env.example when
// missing) at the new database and address.
func prepareEnv(root string, db SiteDatabase, host, url string) ([]string, error) {
	var warnings []string
	env := filepath.Join(root, ".env")
	if !fileExists(env) {
		example := filepath.Join(root, ".env.example")
		if !fileExists(example) {
			return []string{"no .env in the archive: create one for database " + db.Database}, nil
		}
		if err := copyFile(example, env); err != nil {
			return nil, err
		}
		warnings = append(warnings, "the archive had no .env: Benbow made one from .env.example (run `benbow artisan <site> key:generate`)")
	}
	if !fileExists(filepath.Join(root, "vendor")) {
		warnings = append(warnings, "vendor/ is missing: run `benbow composer <site> install`")
	}
	h, p, _ := strings.Cut(host, ":")
	return warnings, editEnvFile(env, []dotenv.Pair{
		{Key: "APP_URL", Value: url}, {Key: "DB_CONNECTION", Value: "mysql"},
		{Key: "DB_HOST", Value: h}, {Key: "DB_PORT", Value: p},
		{Key: "DB_DATABASE", Value: db.Database}, {Key: "DB_USERNAME", Value: db.User}, {Key: "DB_PASSWORD", Value: db.Password},
	})
}

type archivePlan struct {
	root, sql, framework string
	warnings             []string
}

// planArchive finds the site's folder and the dump in an archive's files.
// The folder is the one holding wp-includes/version.php (WordPress),
// artisan (Laravel), or the shallowest index.php; the dump is the largest
// .sql or .sql.gz.
func planArchive(entries []archiveEntry) (archivePlan, error) {
	var p archivePlan
	best := func(match func(string) (string, bool)) (string, bool) {
		found := ""
		ok := false
		for _, e := range entries {
			if dir, m := match(e.name); m && (!ok || pathDepth(dir) < pathDepth(found)) {
				found, ok = dir, true
			}
		}
		return found, ok
	}
	suffix := func(s string) func(string) (string, bool) {
		return func(n string) (string, bool) {
			if n == s {
				return "", true
			}
			dir, ok := strings.CutSuffix(n, "/"+s)
			return dir, ok
		}
	}
	if dir, ok := best(suffix("wp-includes/version.php")); ok {
		p.root, p.framework = dir, FrameworkWordPress
	} else if dir, ok := best(suffix("artisan")); ok {
		p.root, p.framework = dir, FrameworkLaravel
	} else if dir, ok := best(suffix("index.php")); ok {
		p.root = dir
	} else if dir, ok := best(suffix("index.html")); ok {
		p.root = dir
	} else {
		return p, errcode.Errorf(errcode.InvalidInput, "no site found in the archive: it needs a folder with index.php (or WordPress, or a Laravel project)")
	}
	var dumps []archiveEntry
	for _, e := range entries {
		if strings.HasSuffix(e.name, ".sql") || strings.HasSuffix(e.name, ".sql.gz") {
			dumps = append(dumps, e)
		}
	}
	slices.SortFunc(dumps, func(a, b archiveEntry) int { return int(b.size - a.size) })
	if len(dumps) > 0 {
		p.sql = dumps[0].name
		if len(dumps) > 1 {
			var others []string
			for _, d := range dumps[1:] {
				others = append(others, d.name)
			}
			p.warnings = append(p.warnings, "loaded the largest dump, "+p.sql+"; also in the archive: "+strings.Join(others, ", "))
		}
	}
	return p, nil
}

func pathDepth(dir string) int {
	if dir == "" {
		return 0
	}
	return strings.Count(dir, "/") + 1
}

var errStopWalk = errors.New("stop")

// archiveWordPressVersion reads $wp_version from one file of an archive
// without unpacking it.
func archiveWordPressVersion(file, kind, name string) string {
	name = strings.TrimPrefix(name, "/")
	var version string
	_ = walkArchive(file, kind, func(n string, size int64, _ fs.FileMode, r io.Reader) error {
		if n != name || size > 1<<20 {
			return nil
		}
		b, err := io.ReadAll(r)
		if err == nil {
			if m := wpVersionRe.FindSubmatch(b); m != nil {
				version = string(m[1])
			}
		}
		return errStopWalk
	})
	return version
}

var wpVersionRe = regexp.MustCompile(`\$wp_version\s*=\s*'([0-9.]+)'`)

// wordPressVersion reads $wp_version from a WordPress folder.
func wordPressVersion(root string) string {
	b, err := os.ReadFile(filepath.Join(root, "wp-includes", "version.php"))
	if err != nil {
		return ""
	}
	if m := wpVersionRe.FindSubmatch(b); m != nil {
		return string(m[1])
	}
	return ""
}

// phpForArchive picks a PHP version an old WordPress still runs on.
func phpForArchive(wp string) (string, string) {
	if wp == "" {
		return "8.4", "the default"
	}
	switch {
	case store.CompareVersions(wp, "4.0") < 0:
		return "5.6", "WordPress " + wp + " predates PHP 7"
	case store.CompareVersions(wp, "5.3") < 0:
		return "7.4", "WordPress " + wp + " predates PHP 8"
	case store.CompareVersions(wp, "6.3") < 0:
		return "8.1", "WordPress " + wp + " is older than PHP 8.2 support"
	}
	return "8.4", "WordPress " + wp + " runs on current PHP"
}

func archiveKind(file string) (string, error) {
	lower := strings.ToLower(file)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return "zip", nil
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "tgz", nil
	case strings.HasSuffix(lower, ".tar"):
		return "tar", nil
	}
	return "", errcode.Errorf(errcode.Unsupported, "Benbow opens .zip, .tar.gz and .tar archives")
}

var nameJunkRe = regexp.MustCompile(`[^a-z0-9]+`)

// nameFromArchive makes a site name from an archive's file name.
func nameFromArchive(file string) string {
	base := strings.ToLower(filepath.Base(file))
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		base = strings.TrimSuffix(base, ext)
	}
	name := strings.Trim(nameJunkRe.ReplaceAllString(base, "-"), "-")
	if len(name) > 63 {
		name = strings.Trim(name[:63], "-")
	}
	if name == "" {
		name = "imported"
	}
	return name
}

func sqlExt(name string) string {
	if strings.HasSuffix(name, ".gz") {
		return ".sql.gz"
	}
	return ".sql"
}

func emptyOrMissing(dir string) bool {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			return false
		}
	}
	return true
}

// moveTree moves src to dst (missing or empty), copying across volumes.
func moveTree(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(dst), err)
	}
	_ = os.Remove(filepath.Join(dst, ".DS_Store"))
	_ = os.Remove(dst) // empty
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	return copyTree(src, dst)
}

// archiveName checks an entry's name: inside the archive, no backslashes.
// It returns "" for entries to skip (macOS metadata).
func archiveName(raw string) (string, error) {
	name := strings.TrimPrefix(raw, "./")
	if name == "" || name == "." || strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
		return "", nil
	}
	clean := path.Clean(name)
	if path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") {
		return "", errcode.Newf(errcode.InvalidInput, "archive entry %q points outside the archive", raw)
	}
	return clean, nil
}

// listArchive returns an archive's regular files, checking every name.
func listArchive(file, kind string) ([]archiveEntry, error) {
	var out []archiveEntry
	err := walkArchive(file, kind, func(name string, size int64, _ fs.FileMode, _ io.Reader) error {
		out = append(out, archiveEntry{name: name, size: size})
		return nil
	})
	return out, err
}

// extractArchive unpacks regular files and folders into dir. Links and
// special files are skipped.
func extractArchive(file, kind, dir string) error {
	return walkArchive(file, kind, func(name string, size int64, mode fs.FileMode, r io.Reader) error {
		target := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("extract: %w", err)
		}
		perm := os.FileMode(0o644)
		if mode&0o100 != 0 {
			perm = 0o755
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, perm)
		if err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		if _, err := io.Copy(out, io.LimitReader(r, size+1)); err != nil {
			_ = out.Close()
			return fmt.Errorf("extract %s: %w", name, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("extract %s: %w", name, err)
		}
		return nil
	})
}

// walkArchive calls fn for each regular file (r is nil while only listing
// a zip), with the archive's limits enforced.
func walkArchive(file, kind string, fn func(name string, size int64, mode fs.FileMode, r io.Reader) error) error {
	var files int
	var total int64
	check := func(size int64) error {
		files++
		total += size
		if files > maxArchiveFiles {
			return errcode.Errorf(errcode.InvalidInput, "the archive has more than a million files")
		}
		if total > maxArchiveTotal {
			return errcode.Errorf(errcode.InvalidInput, "the archive is larger than 100 GiB unpacked")
		}
		return nil
	}
	if kind == "zip" {
		zr, err := zip.OpenReader(file)
		if err != nil {
			return errcode.New(errcode.InvalidInput, fmt.Errorf("open %s: %w", file, err))
		}
		defer zr.Close()
		for _, f := range zr.File {
			name, err := archiveName(f.Name)
			if err != nil {
				return err
			}
			if name == "" || !f.Mode().IsRegular() {
				continue
			}
			size := int64(f.UncompressedSize64) //nolint:gosec // capped by check
			if err := check(size); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return errcode.New(errcode.InvalidInput, fmt.Errorf("read %s: %w", f.Name, err))
			}
			err = fn(name, size, f.Mode(), rc)
			_ = rc.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	f, err := os.Open(file)
	if err != nil {
		return errcode.New(errcode.NotFound, fmt.Errorf("open %s: %w", file, err))
	}
	defer f.Close()
	var r io.Reader = f
	if kind == "tgz" {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return errcode.New(errcode.InvalidInput, fmt.Errorf("read %s: %w", file, err))
		}
		defer gz.Close()
		r = gz
	}
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return errcode.New(errcode.InvalidInput, fmt.Errorf("read %s: %w", file, err))
		}
		name, err := archiveName(h.Name)
		if err != nil {
			return err
		}
		if name == "" || h.Typeflag != tar.TypeReg {
			continue
		}
		if err := check(h.Size); err != nil {
			return err
		}
		if err := fn(name, h.Size, fs.FileMode(h.Mode), tr); err != nil { //nolint:gosec // permission bits only
			return err
		}
	}
}
