// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package mcpserver exposes Covo to AI agents over the Model Context
// Protocol (stdio). Like the CLI it is a thin client: every tool calls the
// core.
//
// Agents are read-only by default. A tool that changes something needs the
// user's permission (Covo → Settings → AI agents, or `covo mcp allow`) for
// that site, or for everything; before it changes a site Covo takes a
// snapshot of it, and every change (or refused attempt) is logged and shown
// in the app. Changing tools take dry_run to show what they would do.
// Nothing here deletes sites, databases or groups.
package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
	"github.com/getcovo/covo/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxOutput caps the text a tool returns (tools' output, logs).
const maxOutput = 100 << 10

// Open opens the core for one tool call: the daemon, the CLI and the app
// may change state in between.
type Open func(ctx context.Context) (*core.Core, error)

// New returns the MCP server.
func New(open Open) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "covo", Title: "Covo", Version: version.Get().Version}, &mcp.ServerOptions{
		Instructions: "Covo is a local PHP/WordPress/Laravel development environment on this Mac (like MAMP PRO). " +
			"Sites are folders served over HTTPS at local addresses (e.g. https://shop.localhost:8443) with their own PHP version. " +
			"Start with covo_status. Tools that change something are refused unless the user allowed it for that site " +
			"(or for everything) in Covo; use dry_run first to show the user what would happen. Before a site is changed, " +
			"Covo takes a snapshot the user can restore. Covo never deletes project folders.",
	})
	t := tools{open: open}
	ro := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	change := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}
	replace := &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(false)}
	network := &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}

	// Read only.
	mcp.AddTool(s, &mcp.Tool{Name: "covo_status", Title: "Covo status", Annotations: ro,
		Description: "Whether Covo is running, its services, the sites with their addresses, MySQL, PostgreSQL, Redis and Mail."}, t.status)
	mcp.AddTool(s, &mcp.Tool{Name: "list_sites", Title: "List sites", Annotations: ro,
		Description: "Every site: name, folder, framework, PHP version, address, domain and aliases, database, on/off, group."}, t.listSites)
	mcp.AddTool(s, &mcp.Tool{Name: "site_logs", Title: "Site logs", Annotations: ro,
		Description: "The end of a site's PHP error log (kind \"php\") or request log (kind \"access\")."}, t.siteLogs)
	mcp.AddTool(s, &mcp.Tool{Name: "list_php_versions", Title: "PHP versions", Annotations: ro,
		Description: "Installed and available PHP versions, with php.net's support status (active, security, eol)."}, t.listPHP)
	mcp.AddTool(s, &mcp.Tool{Name: "php_compatibility_check", Title: "PHP compatibility check", Annotations: ro,
		Description: "Lists what in a site's code would break on another PHP version (PHPCompatibility). Reads the code only."}, t.phpCheck)
	mcp.AddTool(s, &mcp.Tool{Name: "list_databases", Title: "List databases", Annotations: ro,
		Description: "The databases on Covo's MySQL (engine mysql, default) or PostgreSQL (engine pgsql)."}, t.listDatabases)
	mcp.AddTool(s, &mcp.Tool{Name: "list_snapshots", Title: "List snapshots", Annotations: ro,
		Description: "A site's snapshots (files and database saved by Covo), newest first."}, t.listSnapshots)
	mcp.AddTool(s, &mcp.Tool{Name: "agent_permissions", Title: "What agents may change", Annotations: ro,
		Description: "Which sites AI agents may change (set by the user in Covo; agents cannot change it)."}, t.permissions)

	// Changes: permission, snapshot, log.
	mcp.AddTool(s, &mcp.Tool{Name: "start", Title: "Start Covo", Annotations: change,
		Description: "Starts every enabled site and Covo's services. Needs permission for everything."}, t.start)
	mcp.AddTool(s, &mcp.Tool{Name: "stop", Title: "Stop Covo", Annotations: change,
		Description: "Stops every site and Covo's services. Nothing is deleted. Needs permission for everything."}, t.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "add_site", Title: "Add a site", Annotations: change,
		Description: "Adds a site for a folder (absolute path). Optional: domain, aliases, create_folder, database (\"new\" or an existing name), " +
			"database_engine, php.ini values. Needs permission for everything."}, t.addSite)
	mcp.AddTool(s, &mcp.Tool{Name: "update_site", Title: "Change a site", Annotations: change,
		Description: "Changes a site's PHP version, folder, domain or aliases, or turns it on or off. Snapshot first."}, t.updateSite)
	mcp.AddTool(s, &mcp.Tool{Name: "create_site_database", Title: "Create a site database", Annotations: change,
		Description: "Creates a database with its own user for a site (engine mysql or pgsql); the password stays in the keychain."}, t.createDatabase)
	mcp.AddTool(s, &mcp.Tool{Name: "new_wordpress", Title: "New WordPress site", Annotations: network,
		Description: "Creates a WordPress site in one step; returns the admin password once. Needs permission for everything."}, t.newWordPress)
	mcp.AddTool(s, &mcp.Tool{Name: "new_laravel", Title: "New Laravel project", Annotations: network,
		Description: "Creates a Laravel project with Composer, its database and .env. Needs permission for everything."}, t.newLaravel)
	for _, tool := range []string{core.ToolWP, core.ToolArtisan, core.ToolComposer} {
		mcp.AddTool(s, &mcp.Tool{Name: tool, Title: "Run " + tool, Annotations: network,
			Description: "Runs " + tool + " for a site with its PHP version (args e.g. [\"" + map[string]string{"wp": "plugin", "artisan": "migrate:status", "composer": "show"}[tool] +
				"\"]). Returns output and exit code. A snapshot is taken first; commands can change or delete data."}, t.runTool(tool))
	}
	mcp.AddTool(s, &mcp.Tool{Name: "create_snapshot", Title: "Take a snapshot", Annotations: change,
		Description: "Saves a site's files and database now."}, t.createSnapshot)
	mcp.AddTool(s, &mcp.Tool{Name: "restore_snapshot", Title: "Restore a snapshot", Annotations: replace,
		Description: "Puts a site's files and database back to a snapshot (the current state is saved first)."}, t.restoreSnapshot)
	mcp.AddTool(s, &mcp.Tool{Name: "pull", Title: "Pull from production", Annotations: network,
		Description: "Copies a remote's (production or staging) database and uploads into the site. dry_run shows what would change."}, t.pull)
	mcp.AddTool(s, &mcp.Tool{Name: "import_sites", Title: "Import from another tool", Annotations: change,
		Description: "Imports sites from mamp, local, herd, valet or xampp (read only); migrate also copies databases. dry_run (default true) changes nothing. Needs permission for everything."}, t.importSites)
	return s
}

func ptr[T any](v T) *T { return &v }

type tools struct{ open Open }

// read runs fn on a freshly opened core and returns its result as JSON.
func read[T any](ctx context.Context, open Open, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
	c, err := open(ctx)
	if err != nil {
		return toolError(err), nil, nil
	}
	defer func() { _ = c.Close() }()
	v, err := fn(c)
	if err != nil {
		return toolError(err), nil, nil
	}
	return jsonResult(v), nil, nil
}

// change runs a change for an agent: the user's permission for site ("":
// everything), a snapshot of the site first (unless dryRun, or the site
// does not exist yet, or noSnapshot), the change, and a log entry.
func change[T any](ctx context.Context, open Open, tool, site string, args any, dryRun, noSnapshot bool, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
	c, err := open(ctx)
	if err != nil {
		return toolError(err), nil, nil
	}
	defer func() { _ = c.Close() }()
	argJSON, _ := json.Marshal(args)
	act := store.MCPAction{At: time.Now(), Tool: tool, Site: site, Args: string(argJSON), DryRun: dryRun}
	logged := func(err error) {
		act.OK = err == nil
		if err != nil {
			act.Error = err.Error()
		}
		_ = c.Store.AddMCPAction(context.WithoutCancel(ctx), act)
	}
	if err := c.CheckMCPWrite(ctx, site); err != nil {
		logged(err)
		return toolError(err), nil, nil
	}
	if site != "" && !dryRun && !noSnapshot {
		if _, err := c.Store.Site(ctx, site); err == nil {
			snap, err := c.CreateSnapshot(ctx, site, core.SnapshotMCP, "before "+tool+" (AI agent)")
			if err != nil {
				err = fmt.Errorf("the snapshot before the change failed, nothing was changed: %w", err)
				logged(err)
				return toolError(err), nil, nil
			}
			act.Snapshot = snap.ID
		}
	}
	v, err := fn(c)
	logged(err)
	if err != nil {
		return toolError(err), nil, nil
	}
	out := map[string]any{"result": v}
	if act.Snapshot != "" {
		out["snapshot"] = act.Snapshot
		out["undo"] = "restore_snapshot with id " + act.Snapshot
	}
	if dryRun {
		out["dry_run"] = true
	}
	return jsonResult(out), nil, nil
}

// toolError reports a failure to the agent (not as a protocol error), with
// Covo's error code.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
		&mcp.TextContent{Text: errcode.Code(err) + ": " + err.Error()},
	}}
}

func jsonResult(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(err)
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: clip(string(b))}}}
}

func clip(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return s[:maxOutput] + "\n… (output cut at 100 KiB)"
}

type empty struct{}

type dryIn struct {
	DryRun bool `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) status(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (core.Status, error) { return c.Status(ctx) })
}

func (t tools) listSites(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) ([]core.SiteStatus, error) { return c.ListSites(ctx) })
}

func (t tools) permissions(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (core.MCPPermissions, error) { return c.GetMCPPermissions(ctx) })
}

type siteLogsIn struct {
	Site  string `json:"site" jsonschema:"the site's name"`
	Kind  string `json:"kind,omitempty" jsonschema:"php (errors, the default) or access (requests)"`
	Lines int    `json:"lines,omitempty" jsonschema:"number of lines from the end (default 50, max 2000)"`
}

func (t tools) siteLogs(ctx context.Context, _ *mcp.CallToolRequest, in siteLogsIn) (*mcp.CallToolResult, any, error) {
	kind := in.Kind
	if kind == "" {
		kind = core.LogPHP
	}
	return read(ctx, t.open, func(c *core.Core) (core.SiteLog, error) { return c.SiteLogs(ctx, in.Site, kind, in.Lines) })
}

func (t tools) listPHP(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (core.PHPList, error) { return c.ListPHP(ctx) })
}

type phpCheckIn struct {
	Site   string `json:"site" jsonschema:"the site's name"`
	Target string `json:"target,omitempty" jsonschema:"PHP version to check against, e.g. 8.4 (default: the newest fully supported)"`
	All    bool   `json:"all,omitempty" jsonschema:"WordPress sites: also check the WordPress core (default: wp-content only)"`
}

func (t tools) phpCheck(ctx context.Context, _ *mcp.CallToolRequest, in phpCheckIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (core.PHPCheckResult, error) { return c.CheckPHP(ctx, in.Site, in.Target, in.All) })
}

type engineIn struct {
	Engine string `json:"engine,omitempty" jsonschema:"mysql (default) or pgsql"`
}

func (t tools) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, in engineIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) ([]string, error) {
		if in.Engine == core.EnginePostgres {
			return c.PostgresDatabases(ctx)
		}
		return c.Databases(ctx)
	})
}

type siteIn struct {
	Site string `json:"site" jsonschema:"the site's name"`
}

func (t tools) listSnapshots(ctx context.Context, _ *mcp.CallToolRequest, in siteIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) ([]store.Snapshot, error) { return c.ListSnapshots(ctx, in.Site) })
}

func (t tools) start(ctx context.Context, _ *mcp.CallToolRequest, in dryIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t.open, "start", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would start every enabled site", nil
		}
		return c.Start(ctx, 0)
	})
}

func (t tools) stop(ctx context.Context, _ *mcp.CallToolRequest, in dryIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t.open, "stop", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would stop every site", nil
		}
		return c.Stop(ctx)
	})
}

type addSiteIn struct {
	Name           string            `json:"name" jsonschema:"site name: lowercase letters, digits and dashes"`
	Path           string            `json:"path" jsonschema:"absolute path of the folder (a Laravel project is served from public/)"`
	PHP            string            `json:"php,omitempty" jsonschema:"PHP version, e.g. 8.4 (default 8.4)"`
	Domain         string            `json:"domain,omitempty" jsonschema:"local domain under .localhost (or .test/.local once enabled); default <name>.localhost"`
	Aliases        []string          `json:"aliases,omitempty" jsonschema:"extra domains"`
	CreateFolder   bool              `json:"create_folder,omitempty" jsonschema:"create the folder (missing or empty) for a new empty site"`
	Database       string            `json:"database,omitempty" jsonschema:"\"new\" for a database with its own user, or an existing database's name"`
	DatabaseEngine string            `json:"database_engine,omitempty" jsonschema:"mysql (default) or pgsql"`
	PHPSettings    map[string]string `json:"php_settings,omitempty" jsonschema:"php.ini values: memory_limit, upload_max_filesize, max_execution_time, display_errors"`
	Force          bool              `json:"force,omitempty" jsonschema:"accept a folder without index.php or index.html"`
	DryRun         bool              `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) addSite(ctx context.Context, _ *mcp.CallToolRequest, in addSiteIn) (*mcp.CallToolResult, any, error) {
	if !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.AddSiteRequest{
		Name: in.Name, Path: filepath.Clean(in.Path), PHP: in.PHP, Domain: in.Domain, Aliases: in.Aliases,
		CreateFolder: in.CreateFolder, PHPSettings: in.PHPSettings, Force: in.Force, DatabaseEngine: in.DatabaseEngine,
	}
	if req.PHP == "" {
		req.PHP = "8.4"
	}
	switch in.Database {
	case "":
	case "new":
		req.NewDatabase = true
	default:
		req.LinkDatabase = in.Database
	}
	return change(ctx, t.open, "add_site", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return map[string]any{"would_add": req.Name, "folder": req.Path, "php": req.PHP, "domain": req.Domain, "database": in.Database}, nil
		}
		return c.AddSite(ctx, req)
	})
}

type updateSiteIn struct {
	Name    string    `json:"name" jsonschema:"the site's name"`
	PHP     string    `json:"php,omitempty" jsonschema:"new PHP version, e.g. 8.3"`
	Path    string    `json:"path,omitempty" jsonschema:"new document root (absolute)"`
	Domain  string    `json:"domain,omitempty" jsonschema:"new local domain"`
	Aliases *[]string `json:"aliases,omitempty" jsonschema:"replace the extra domains (empty list clears them)"`
	Enabled *bool     `json:"enabled,omitempty" jsonschema:"serve the site (true) or not (false)"`
	Force   bool      `json:"force,omitempty" jsonschema:"accept a folder without index.php or index.html"`
	DryRun  bool      `json:"dry_run,omitempty" jsonschema:"only say what would change"`
}

func (t tools) updateSite(ctx context.Context, _ *mcp.CallToolRequest, in updateSiteIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.UpdateSiteRequest{Name: in.Name, PHP: in.PHP, Domain: in.Domain, Aliases: in.Aliases, Enabled: in.Enabled, Force: in.Force}
	if in.Path != "" {
		req.Path = filepath.Clean(in.Path)
	}
	return change(ctx, t.open, "update_site", in.Name, in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			cur, err := c.Store.Site(ctx, in.Name)
			return map[string]any{"now": cur, "would_change": in}, err
		}
		return c.UpdateSite(ctx, req)
	})
}

type createDBIn struct {
	Site   string `json:"site" jsonschema:"the site's name"`
	Engine string `json:"engine,omitempty" jsonschema:"mysql (default) or pgsql"`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in createDBIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t.open, "create_site_database", in.Site, in, in.DryRun, true, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would create database " + core.DefaultDatabaseName(in.Site) + " with its own user", nil
		}
		return c.CreateSiteDatabaseOn(ctx, in.Site, "", in.Engine)
	})
}

type wordpressIn struct {
	Name       string `json:"name" jsonschema:"site name: lowercase letters, digits and dashes"`
	Path       string `json:"path,omitempty" jsonschema:"new or empty folder (absolute); default ~/Sites/<name>"`
	PHP        string `json:"php,omitempty" jsonschema:"PHP version (default 8.4)"`
	Title      string `json:"title,omitempty" jsonschema:"site title"`
	AdminUser  string `json:"admin_user,omitempty" jsonschema:"administrator user (default admin)"`
	AdminEmail string `json:"admin_email,omitempty" jsonschema:"administrator email"`
	Domain     string `json:"domain,omitempty" jsonschema:"local domain (default <name>.localhost)"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) newWordPress(ctx context.Context, _ *mcp.CallToolRequest, in wordpressIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.WordPressRequest{Name: in.Name, Path: in.Path, PHP: in.PHP, Title: in.Title,
		AdminUser: in.AdminUser, AdminEmail: in.AdminEmail, Domain: in.Domain}
	return change(ctx, t.open, "new_wordpress", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would create WordPress site " + in.Name, nil
		}
		return c.NewWordPress(ctx, req)
	})
}

type laravelIn struct {
	Name     string `json:"name" jsonschema:"site name: lowercase letters, digits and dashes"`
	Path     string `json:"path,omitempty" jsonschema:"new or empty folder (absolute); default ~/Sites/<name>"`
	PHP      string `json:"php,omitempty" jsonschema:"PHP version, 8.2 or newer (default 8.4)"`
	Database string `json:"database,omitempty" jsonschema:"new (own database) or sqlite"`
	Engine   string `json:"engine,omitempty" jsonschema:"mysql or pgsql for a new database"`
	DryRun   bool   `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) newLaravel(ctx context.Context, _ *mcp.CallToolRequest, in laravelIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.LaravelRequest{Name: in.Name, Path: in.Path, PHP: in.PHP, Database: in.Database, Engine: in.Engine}
	return change(ctx, t.open, "new_laravel", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would create Laravel project " + in.Name, nil
		}
		return c.NewLaravel(ctx, req)
	})
}

type toolIn struct {
	Site   string   `json:"site" jsonschema:"the site's name"`
	Args   []string `json:"args" jsonschema:"the tool's arguments"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"only show the command that would run"`
}

// toolTimeout bounds one wp-cli, Composer or Artisan run.
const toolTimeout = 10 * time.Minute

func (t tools) runTool(tool string) func(context.Context, *mcp.CallToolRequest, toolIn) (*mcp.CallToolResult, any, error) {
	return func(ctx context.Context, _ *mcp.CallToolRequest, in toolIn) (*mcp.CallToolResult, any, error) {
		if len(in.Args) == 0 {
			return toolError(errcode.Errorf(errcode.InvalidInput, "args: give a command")), nil, nil
		}
		for _, a := range in.Args {
			// They need a terminal to answer questions.
			if strings.HasPrefix(a, "--prompt") || (tool == core.ToolWP && a == "shell") || (tool == core.ToolArtisan && a == "tinker") {
				return toolError(errcode.Errorf(errcode.Unsupported, fmt.Sprintf("%q needs a terminal: run `covo %s %s …` yourself", a, tool, in.Site))), nil, nil
			}
		}
		ctx, cancel := context.WithTimeout(ctx, toolTimeout)
		defer cancel()
		return change(ctx, t.open, tool, in.Site, in, in.DryRun, false, func(c *core.Core) (any, error) {
			if in.DryRun {
				return tool + " " + strings.Join(in.Args, " "), nil
			}
			var out, errs bytes.Buffer
			code, err := c.RunTool(ctx, in.Site, tool, in.Args, strings.NewReader(""), &out, &errs)
			return map[string]any{"exit_code": code, "output": clip(out.String()), "errors": clip(errs.String())}, err
		})
	}
}

type noteIn struct {
	Site string `json:"site" jsonschema:"the site's name"`
	Note string `json:"note,omitempty" jsonschema:"what the snapshot is for"`
}

func (t tools) createSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in noteIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t.open, "create_snapshot", in.Site, in, false, true, func(c *core.Core) (store.Snapshot, error) {
		return c.CreateSnapshot(ctx, in.Site, core.SnapshotMCP, in.Note)
	})
}

type restoreIn struct {
	Site   string `json:"site" jsonschema:"the site's name"`
	ID     string `json:"id" jsonschema:"the snapshot's id (list_snapshots)"`
	DryRun bool   `json:"dry_run,omitempty" jsonschema:"only show the snapshot"`
}

func (t tools) restoreSnapshot(ctx context.Context, _ *mcp.CallToolRequest, in restoreIn) (*mcp.CallToolResult, any, error) {
	// RestoreSnapshot saves the current state itself.
	return change(ctx, t.open, "restore_snapshot", in.Site, in, in.DryRun, true, func(c *core.Core) (any, error) {
		if in.DryRun {
			return c.Store.Snapshot(ctx, in.Site, in.ID)
		}
		return c.RestoreSnapshot(ctx, in.Site, in.ID)
	})
}

type pullIn struct {
	Site       string `json:"site" jsonschema:"the site's name"`
	Remote     string `json:"remote,omitempty" jsonschema:"the remote's name (default production)"`
	NoFiles    bool   `json:"no_files,omitempty" jsonschema:"leave the files out"`
	NoDatabase bool   `json:"no_database,omitempty" jsonschema:"leave the database out"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"only say what would change"`
}

func (t tools) pull(ctx context.Context, _ *mcp.CallToolRequest, in pullIn) (*mcp.CallToolResult, any, error) {
	if in.Remote == "" {
		in.Remote = core.RemoteProduction
	}
	// Pull takes its own snapshot first.
	return change(ctx, t.open, "pull", in.Site, in, in.DryRun, true, func(c *core.Core) (core.SyncResult, error) {
		return c.Pull(ctx, in.Site, in.Remote, core.SyncOptions{DryRun: in.DryRun, NoFiles: in.NoFiles, NoDatabase: in.NoDatabase})
	})
}

type importIn struct {
	Source    string   `json:"source" jsonschema:"mamp, local, herd, valet or xampp"`
	Sites     []string `json:"sites,omitempty" jsonschema:"only these hosts"`
	Localhost bool     `json:"localhost,omitempty" jsonschema:"serve them as <name>.localhost"`
	Migrate   bool     `json:"migrate,omitempty" jsonschema:"also copy databases and point wp-config.php/.env at Covo"`
	DryRun    *bool    `json:"dry_run,omitempty" jsonschema:"only say what would happen (default true)"`
}

func (t tools) importSites(ctx context.Context, _ *mcp.CallToolRequest, in importIn) (*mcp.CallToolResult, any, error) {
	dry := in.DryRun == nil || *in.DryRun
	return change(ctx, t.open, "import_sites", "", in, dry, false, func(c *core.Core) (core.ImportPlan, error) {
		return c.Import(ctx, core.ImportOptions{Source: in.Source, Sites: in.Sites, Localhost: in.Localhost, Migrate: in.Migrate, DryRun: dry})
	})
}
