// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/dotenv"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/store"
)

// Tools a site can run with its own PHP version.
const (
	ToolWP       = "wp"
	ToolComposer = "composer"
	ToolArtisan  = "artisan"
)

// composerFor picks Composer for a PHP version: the current one, or the
// 2.2 LTS line for PHP older than 7.2.5.
func composerFor(minor string) string {
	if compareMinor(minor, "7.3") >= 0 {
		return "composer"
	}
	return "composer-lts"
}

// composerCommand prepares Composer with minor's PHP in dir; its home and
// cache live inside the Covo folder, not in ~/.composer.
func (c *Core) composerCommand(ctx context.Context, minor, dir string, args ...string) (*exec.Cmd, error) {
	art, err := c.pinned(ctx, composerFor(minor), "")
	if err != nil {
		return nil, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return nil, err
	}
	cmd, err := c.phpToolCommand(ctx, minor, dir, filepath.Join(inst.Dir, art.Files[0]), args...)
	if err != nil {
		return nil, err
	}
	home := filepath.Join(c.Layout.Data, "composer")
	cmd.Env = append(cmd.Env, "COMPOSER_HOME="+home, "COMPOSER_CACHE_DIR="+filepath.Join(home, "cache"))
	return cmd, nil
}

// toolCommand prepares wp-cli, Composer or Artisan for a site.
func (c *Core) toolCommand(ctx context.Context, site store.Site, tool string, args []string) (*exec.Cmd, error) {
	switch tool {
	case ToolWP:
		return c.wpCommand(ctx, site, args...)
	case ToolComposer:
		return c.composerCommand(ctx, site.PHPMinor, projectRoot(site), args...)
	case ToolArtisan:
		root := projectRoot(site)
		if _, err := os.Stat(filepath.Join(root, "artisan")); err != nil {
			return nil, errcode.Errorf(errcode.Unsupported, fmt.Sprintf("%s is not a Laravel project (no artisan in %s)", site.Name, root))
		}
		return c.phpToolCommand(ctx, site.PHPMinor, root, "artisan", args...)
	default:
		return nil, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("unknown tool %q (wp, composer, artisan)", tool))
	}
}

// RunTool runs wp-cli, Composer or Artisan for a site with the given
// arguments and streams, and returns the tool's exit code.
func (c *Core) RunTool(ctx context.Context, name, tool string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return 1, err
	}
	cmd, err := c.toolCommand(ctx, site, tool, args)
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
		return 1, errcode.New(errcode.Service, fmt.Errorf("run %s: %w", tool, err))
	}
	return 0, nil
}

// ToolResult is the outcome of a tool run through the API.
type ToolResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Errors   string `json:"errors,omitempty"`
}

// maxToolOutput caps what RunToolCaptured keeps of each stream.
const maxToolOutput = 256 << 10

// RunToolCaptured runs a tool without input and returns its output (cut at
// 256 KiB per stream).
func (c *Core) RunToolCaptured(ctx context.Context, name, tool string, args []string) (ToolResult, error) {
	var out, errs capped
	code, err := c.RunTool(ctx, name, tool, args, strings.NewReader(""), &out, &errs)
	return ToolResult{ExitCode: code, Output: out.String(), Errors: errs.String()}, err
}

type capped struct{ strings.Builder }

func (b *capped) Write(p []byte) (int, error) {
	if room := maxToolOutput - b.Len(); room > 0 {
		_, _ = b.Builder.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}

// LaravelRequest describes a new Laravel project.
type LaravelRequest struct {
	Name string
	// Path is the project folder: missing or empty. Default ~/Sites/<name>.
	Path string
	PHP  string // default 8.4; Laravel needs 8.2 or newer
	// Database is "new" (the default when MySQL is running: a database
	// with its own user) or "sqlite" (Laravel's own default).
	Database string
	// Engine of a new database: "mysql" or "pgsql" (default: MySQL if it
	// is running, else PostgreSQL).
	Engine      string
	Domain      string
	Aliases     []string
	Group       int64
	PHPSettings map[string]string
}

// LaravelResult is the new project.
type LaravelResult struct {
	SiteStatus
	Laravel  string `json:"laravel"`
	Database string `json:"database"` // database name, or "sqlite"
}

// minLaravelPHP is what the current Laravel release needs.
const minLaravelPHP = "8.2"

// NewLaravel creates a Laravel project with Composer, adds it as a site
// served from public/, and points its .env at the site: APP_URL, the site
// database (or SQLite) and Mailpit. On failure everything it created is
// removed again.
func (c *Core) NewLaravel(ctx context.Context, req LaravelRequest) (LaravelResult, error) {
	var res LaravelResult
	if !siteNameRe.MatchString(req.Name) {
		return res, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("site name %q must be lowercase letters, digits and dashes", req.Name))
	}
	if req.PHP == "" {
		req.PHP = "8.4"
	}
	if err := validMinor(req.PHP); err != nil {
		return res, err
	}
	if compareMinor(req.PHP, minLaravelPHP) < 0 {
		return res, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("Laravel needs PHP %s or newer, not %s", minLaravelPHP, req.PHP))
	}
	_, _, _, mysqlErr := c.mysqlRoot(ctx)
	_, _, _, pgErr := c.postgresRoot(ctx)
	if req.Engine == "" {
		req.Engine = EngineMySQL
		if mysqlErr != nil && pgErr == nil {
			req.Engine = EnginePostgres
		}
	}
	engine, err := checkEngine(req.Engine)
	if err != nil {
		return res, err
	}
	req.Engine = engine
	if req.Database == "" {
		req.Database = "sqlite"
		if (engine == EngineMySQL && mysqlErr == nil) || (engine == EnginePostgres && pgErr == nil) {
			req.Database = "new"
		}
	}
	if req.Database != "new" && req.Database != "sqlite" {
		return res, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("database must be new or sqlite, got %q", req.Database))
	}
	if req.Database == "new" {
		if engine == EnginePostgres && pgErr != nil {
			return res, pgErr
		}
		if engine == EngineMySQL && mysqlErr != nil {
			return res, mysqlErr
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return res, fmt.Errorf("resolve home directory: %w", err)
	}
	if req.Path == "" {
		req.Path = filepath.Join(home, "Sites", req.Name)
	}
	if _, err := c.Store.Site(ctx, req.Name); err == nil {
		return res, errcode.Errorf(errcode.Conflict, fmt.Sprintf("site %q already exists", req.Name))
	}
	created, err := prepareEmptyDir(req.Path)
	if err != nil {
		return res, err
	}
	if err := checkSiteDir(req.Path, home, c.Layout.Root); err != nil {
		cleanupDir(req.Path, created)
		return res, err
	}
	added := false
	undo := func() {
		if added {
			_, _ = c.RemoveSiteAndDatabase(context.WithoutCancel(ctx), req.Name)
		}
		cleanupDir(req.Path, created)
	}

	cmd, err := c.composerCommand(ctx, req.PHP, filepath.Dir(req.Path),
		"create-project", "laravel/laravel", req.Path, "--no-interaction", "--prefer-dist", "--no-progress")
	if err != nil {
		undo()
		return res, err
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		undo()
		return res, errcode.New(errcode.Service, fmt.Errorf("composer create-project: %w: %s", err, lastChars(string(out), 2000)))
	}

	site, err := c.AddSite(ctx, AddSiteRequest{
		Name: req.Name, Domain: req.Domain, PHP: req.PHP, Path: req.Path, Aliases: req.Aliases,
		Group: req.Group, PHPSettings: req.PHPSettings, NewDatabase: req.Database == "new", DatabaseEngine: req.Engine,
	})
	if err != nil {
		undo()
		return res, err
	}
	added = true
	res = LaravelResult{SiteStatus: site.SiteStatus, Database: "sqlite"}
	pairs := []dotenv.Pair{{Key: "APP_NAME", Value: req.Name}, {Key: "APP_URL", Value: site.URL}}
	if req.Database == "new" {
		db, err := c.GetSiteDatabase(ctx, req.Name)
		if err != nil {
			undo()
			return res, err
		}
		res.Database = db.Database
		conn, host := "mysql", "127.0.0.1"
		if db.Engine == EnginePostgres {
			// PostgreSQL through Covo's Unix socket: PDO pgsql in the static
			// PHP 8 builds (Swoole's driver) hangs over TCP.
			conn, host = "pgsql", db.Socket
		}
		pairs = append(pairs,
			dotenv.Pair{Key: "DB_CONNECTION", Value: conn},
			dotenv.Pair{Key: "DB_HOST", Value: host},
			dotenv.Pair{Key: "DB_PORT", Value: strconv.Itoa(db.Port)},
			dotenv.Pair{Key: "DB_DATABASE", Value: db.Database},
			dotenv.Pair{Key: "DB_USERNAME", Value: db.User},
			dotenv.Pair{Key: "DB_PASSWORD", Value: db.Password},
		)
	}
	if r, err := c.redisStatus(ctx); err == nil && r.Enabled {
		pairs = append(pairs,
			dotenv.Pair{Key: "REDIS_CLIENT", Value: "phpredis"},
			dotenv.Pair{Key: "REDIS_HOST", Value: "127.0.0.1"},
			dotenv.Pair{Key: "REDIS_PORT", Value: strconv.Itoa(r.Port)},
			dotenv.Pair{Key: "CACHE_STORE", Value: "redis"},
		)
	}
	if mail, err := c.Mail(ctx); err == nil && mail.Enabled {
		pairs = append(pairs,
			dotenv.Pair{Key: "MAIL_MAILER", Value: "smtp"},
			dotenv.Pair{Key: "MAIL_HOST", Value: "127.0.0.1"},
			dotenv.Pair{Key: "MAIL_PORT", Value: strconv.Itoa(mail.SMTPPort)},
		)
	}
	if err := editEnvFile(filepath.Join(req.Path, ".env"), pairs); err != nil {
		undo()
		return res, err
	}
	stored, err := c.Store.Site(ctx, req.Name)
	if err != nil {
		undo()
		return res, err
	}
	res.Site = stored
	if req.Database == "new" {
		if r, err := c.RunToolCaptured(ctx, req.Name, ToolArtisan, []string{"migrate", "--force", "--no-interaction"}); err != nil || r.ExitCode != 0 {
			undo()
			if err == nil {
				err = errcode.Errorf(errcode.Service, "artisan migrate failed: "+lastChars(r.Output+r.Errors, 2000))
			}
			return res, err
		}
	}
	if r, err := c.RunToolCaptured(ctx, req.Name, ToolArtisan, []string{"--version"}); err == nil {
		res.Laravel = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Output), "Laravel Framework"))
	}
	return res, nil
}

// editEnvFile sets keys in a .env file, keeping its mode.
func editEnvFile(path string, pairs []dotenv.Pair) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return rewriteProjectFile(path, []byte(dotenv.Set(string(b), pairs)))
}

// rewriteProjectFile replaces a file in the user's project (.env,
// wp-config.php) all at once, so a crash or a full disk never leaves half
// of it. It keeps the file's mode and writes through a symbolic link to
// its target instead of replacing the link.
func rewriteProjectFile(path string, body []byte) error {
	target, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	_, err = fsutil.WriteAtomic(target, body, info.Mode().Perm())
	return err
}

// lastChars keeps the end of a long tool output.
func lastChars(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
