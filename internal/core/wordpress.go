// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/php"
	"github.com/getcovo/covo/internal/store"
)

// ensurePHPCLI installs the command-line PHP matching the site's PHP-FPM
// build exactly and returns its path.
func (c *Core) ensurePHPCLI(ctx context.Context, v store.PHPVersion) (string, error) {
	art, err := c.Manifest.ResolveFor("php-cli", v.Version)
	if err != nil {
		return "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(inst.Dir, "php")
	out, err := runQuiet(ctx, bin, "-n", "-v")
	if err != nil {
		return "", errcode.New(errcode.Service, fmt.Errorf("run %s -v: %w", bin, err))
	}
	if !strings.HasPrefix(out, "PHP "+v.Version+" ") {
		return "", errcode.Errorf(errcode.Checksum, fmt.Sprintf("php reports %q, expected PHP %s", firstLine(out), v.Version))
	}
	return bin, nil
}

// wpCommand prepares wp-cli for a site: its PHP version, Covo's php.ini
// (MySQL socket, CA bundle) without Xdebug, and wp-cli's cache inside the
// Covo folder.
func (c *Core) wpCommand(ctx context.Context, site store.Site, args ...string) (*exec.Cmd, error) {
	v, err := c.ensurePHP(ctx, site.PHPMinor)
	if err != nil {
		return nil, err
	}
	bin, err := c.ensurePHPCLI(ctx, v)
	if err != nil {
		return nil, err
	}
	art, err := c.pinned(ctx, "wpcli", "")
	if err != nil {
		return nil, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return nil, err
	}
	if _, err := c.ensureCABundle(ctx); err != nil {
		return nil, err
	}
	cfg, err := c.phpINIConfig(ctx, v)
	if err != nil {
		return nil, err
	}
	ini, err := php.INI(cfg)
	if err != nil {
		return nil, err
	}
	iniPath := filepath.Join(c.Layout.Etc, "php", v.Minor, "php-cli.ini")
	if _, err := fsutil.WriteAtomic(iniPath, []byte(ini), 0o644); err != nil {
		return nil, err
	}
	data := filepath.Join(c.Layout.Data, "wp-cli")
	argv := append([]string{"-c", iniPath, "-d", "display_errors=stderr", "-d", "memory_limit=-1",
		filepath.Join(inst.Dir, art.Files[0]), "--path=" + site.Path}, args...)
	cmd := exec.CommandContext(ctx, bin, argv...)
	cmd.Dir = site.Path
	cmd.Env = append(os.Environ(),
		"PHP_INI_SCAN_DIR=",
		"WP_CLI_CACHE_DIR="+filepath.Join(data, "cache"),
		"WP_CLI_PACKAGES_DIR="+filepath.Join(data, "packages"),
		"WP_CLI_CONFIG_PATH="+filepath.Join(data, "config.yml"),
	)
	return cmd, nil
}

// RunWP runs wp-cli for a site with the given arguments and streams. It
// returns wp-cli's exit code.
func (c *Core) RunWP(ctx context.Context, name string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return 1, err
	}
	cmd, err := c.wpCommand(ctx, site, args...)
	if err != nil {
		return 1, err
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, stderr
	err = cmd.Run()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), nil
	}
	if err != nil {
		return 1, errcode.New(errcode.Service, fmt.Errorf("run wp-cli: %w", err))
	}
	return 0, nil
}

// wp runs wp-cli quietly, feeding secret (if any) on stdin for --prompt.
func (c *Core) wp(ctx context.Context, site store.Site, secret string, args ...string) error {
	cmd, err := c.wpCommand(ctx, site, args...)
	if err != nil {
		return err
	}
	var out strings.Builder
	cmd.Stdin = strings.NewReader(secret + "\n")
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if secret != "" {
			msg = strings.ReplaceAll(msg, secret, "…")
		}
		return errcode.New(errcode.Service, fmt.Errorf("wp %s: %w: %s", args[0]+" "+args[1], err, msg))
	}
	return nil
}

// WordPressRequest describes a new WordPress site.
type WordPressRequest struct {
	Name string
	// Path is the new site's folder: missing or empty. Default ~/Sites/<name>.
	Path       string
	PHP        string
	Title      string
	AdminUser  string
	AdminEmail string
}

// WordPressResult is the new site and how to log in. The admin password is
// shown once: Covo does not keep it.
type WordPressResult struct {
	SiteStatus
	WordPress     string `json:"wordpress"`
	Database      string `json:"database"`
	AdminURL      string `json:"admin_url"`
	AdminUser     string `json:"admin_user"`
	AdminPassword string `json:"admin_password"`
}

// NewWordPress creates a folder with the pinned WordPress, a site, a
// database with its own user, wp-config.php, and runs the installation.
// On failure everything it created is removed again.
func (c *Core) NewWordPress(ctx context.Context, req WordPressRequest) (WordPressResult, error) {
	var res WordPressResult
	if !siteNameRe.MatchString(req.Name) {
		return res, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("site name %q must be lowercase letters, digits and dashes", req.Name))
	}
	if req.PHP == "" {
		req.PHP = "8.4"
	}
	if err := validMinor(req.PHP); err != nil {
		return res, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return res, fmt.Errorf("resolve home directory: %w", err)
	}
	if req.Path == "" {
		req.Path = filepath.Join(home, "Sites", req.Name)
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = req.Name
	}
	if req.AdminUser == "" {
		req.AdminUser = "admin"
	}
	if !mysql.ValidName(req.AdminUser, 60) {
		return res, errcode.Errorf(errcode.InvalidInput, "admin user must be lowercase letters, digits and underscores")
	}
	if req.AdminEmail == "" {
		req.AdminEmail = "admin@" + req.Name + "." + suffixLocalhost
	}
	if strings.ContainsAny(req.AdminEmail, " \n\"'") || !strings.Contains(req.AdminEmail, "@") {
		return res, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("admin email %q is not valid", req.AdminEmail))
	}
	if _, err := c.Store.Site(ctx, req.Name); err == nil {
		return res, errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", req.Name))
	}
	if _, _, _, err := c.mysqlRoot(ctx); err != nil {
		return res, err
	}
	created, err := prepareEmptyDir(req.Path)
	if err != nil {
		return res, err
	}
	if err := checkSiteDir(req.Path, home, c.Layout.Root); err != nil {
		cleanupDir(req.Path, created)
		return res, err
	}

	var site SiteResult
	undo := func() {
		if site.Name != "" {
			_, _ = c.UnlinkSiteDatabase(context.WithoutCancel(ctx), req.Name, true)
			_, _ = c.RemoveSite(context.WithoutCancel(ctx), req.Name)
		}
		cleanupDir(req.Path, created)
	}
	art, err := c.Manifest.Resolve("wordpress", "")
	if err != nil {
		undo()
		return res, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		undo()
		return res, err
	}
	if err := copyTree(inst.Dir, req.Path); err != nil {
		undo()
		return res, err
	}
	if site, err = c.AddSite(ctx, AddSiteRequest{Name: req.Name, PHP: req.PHP, Path: req.Path}); err != nil {
		undo()
		return res, err
	}
	db, err := c.CreateSiteDatabase(ctx, req.Name, "")
	if err != nil {
		undo()
		return res, err
	}
	stored, err := c.Store.Site(ctx, req.Name)
	if err != nil {
		undo()
		return res, err
	}
	if err := c.wp(ctx, stored, db.Password, "config", "create", "--dbname="+db.Database, "--dbuser="+db.User,
		"--dbhost=localhost", "--dbcharset=utf8mb4", "--prompt=dbpass"); err != nil {
		undo()
		return res, err
	}
	adminPW := mysql.NewPassword()
	if err := c.wp(ctx, stored, adminPW, "core", "install", "--url="+site.URL, "--title="+req.Title,
		"--admin_user="+req.AdminUser, "--admin_email="+req.AdminEmail, "--skip-email", "--prompt=admin_password"); err != nil {
		undo()
		return res, err
	}
	res = WordPressResult{
		SiteStatus: SiteStatus{Site: stored, URL: site.URL}, WordPress: art.Version, Database: db.Database,
		AdminURL: site.URL + "/wp-admin/", AdminUser: req.AdminUser, AdminPassword: adminPW,
	}
	return res, nil
}

// prepareEmptyDir creates path, or accepts an existing empty folder. It
// reports whether it created it.
func prepareEmptyDir(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(path, 0o755); err != nil {
			return false, fmt.Errorf("create %s: %w", path, err)
		}
		return true, nil
	case err != nil:
		return false, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("%s is not a folder Covo can use: %v", path, err))
	}
	for _, e := range entries {
		if e.Name() != ".DS_Store" {
			return false, errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s is not empty: choose a new or empty folder", path))
		}
	}
	return false, nil
}

// cleanupDir empties a folder that was empty or missing before.
func cleanupDir(path string, created bool) {
	if created {
		_ = os.RemoveAll(path)
		return
	}
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(path, e.Name()))
	}
}

// copyTree copies regular files and folders from src into dst.
func copyTree(src, dst string) error {
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("relative path: %w", err)
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return fmt.Errorf("open: %w", err)
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			return fmt.Errorf("create: %w", err)
		}
		if _, err := io.Copy(out, in); err != nil {
			_ = out.Close()
			return fmt.Errorf("copy %s: %w", rel, err)
		}
		return out.Close()
	})
	if err != nil {
		return fmt.Errorf("copy WordPress: %w", err)
	}
	return nil
}
