// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package mcpserver exposes Covo to AI agents over the Model Context
// Protocol (stdio): sites, PHP versions, logs, databases and wp-cli. Like
// the CLI it is a thin client: every tool calls the core.
//
// Nothing here deletes: removing sites or databases stays with the user
// (CLI or app). Tools that change state say so in their annotations.
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
	"github.com/getcovo/covo/internal/version"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxOutput caps the text a tool returns (wp-cli, logs).
const maxOutput = 100 << 10

// Open opens the core for one tool call: the daemon, the CLI and the app
// may change state in between.
type Open func(ctx context.Context) (*core.Core, error)

// New returns the MCP server.
func New(open Open) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "covo", Title: "Covo", Version: version.Get().Version}, &mcp.ServerOptions{
		Instructions: "Covo is a local PHP/WordPress development environment on this Mac (like MAMP PRO). " +
			"Sites are folders served over HTTPS at local addresses (e.g. https://shop.localhost:8443) with their own PHP version. " +
			"Use covo_status first. Changes apply at once when Covo is running. Folders are never deleted by Covo.",
	})
	t := tools{open: open}
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: ptr(false)}
	change := &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(false)}

	mcp.AddTool(s, &mcp.Tool{Name: "covo_status", Title: "Covo status",
		Description: "Whether Covo is running, its services, the sites with their addresses, PHP versions, MySQL and Mail.",
		Annotations: readOnly}, t.status)
	mcp.AddTool(s, &mcp.Tool{Name: "list_sites", Title: "List sites",
		Description: "Every site: name, folder, PHP version, address, domain and aliases, database, on/off, group.",
		Annotations: readOnly}, t.listSites)
	mcp.AddTool(s, &mcp.Tool{Name: "site_logs", Title: "Site logs",
		Description: "The end of a site's PHP error log (kind \"php\") or request log (kind \"access\").",
		Annotations: readOnly}, t.siteLogs)
	mcp.AddTool(s, &mcp.Tool{Name: "list_php_versions", Title: "PHP versions",
		Description: "Installed and available PHP versions, with php.net's support status (active, security, eol).",
		Annotations: readOnly}, t.listPHP)
	mcp.AddTool(s, &mcp.Tool{Name: "php_compatibility_check", Title: "PHP compatibility check",
		Description: "Lists what in a site's code would break on another PHP version (PHPCompatibility). " +
			"Reads the code only; can take a minute or two on a large site.",
		Annotations: readOnly}, t.phpCheck)
	mcp.AddTool(s, &mcp.Tool{Name: "list_databases", Title: "List databases",
		Description: "The MySQL databases on Covo's MySQL server (MySQL must be on and Covo running).",
		Annotations: readOnly}, t.listDatabases)

	mcp.AddTool(s, &mcp.Tool{Name: "start", Title: "Start Covo",
		Description: "Starts every enabled site and Covo's services. Running it again changes nothing.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true, OpenWorldHint: ptr(false)}}, t.start)
	mcp.AddTool(s, &mcp.Tool{Name: "stop", Title: "Stop Covo",
		Description: "Stops every site and Covo's services. Nothing is deleted.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), IdempotentHint: true, OpenWorldHint: ptr(false)}}, t.stop)
	mcp.AddTool(s, &mcp.Tool{Name: "add_site", Title: "Add a site",
		Description: "Adds a site for a folder (absolute path) with a PHP version. Optional: domain (default <name>.localhost), " +
			"aliases, create_folder (new empty folder), database (\"new\", or an existing database's name), php.ini values.",
		Annotations: change}, t.addSite)
	mcp.AddTool(s, &mcp.Tool{Name: "update_site", Title: "Change a site",
		Description: "Changes a site's PHP version, folder, domain or aliases, or turns it on or off. Omitted fields stay as they are.",
		Annotations: change}, t.updateSite)
	mcp.AddTool(s, &mcp.Tool{Name: "create_site_database", Title: "Create a site database",
		Description: "Creates a MySQL database with its own user for a site; the password stays in the macOS keychain and is returned once.",
		Annotations: change}, t.createDatabase)
	mcp.AddTool(s, &mcp.Tool{Name: "new_wordpress", Title: "New WordPress site",
		Description: "Creates a WordPress site in one step: folder (new or empty), database, wp-config.php, installation. " +
			"Returns the admin password once: Covo does not keep it.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(false), OpenWorldHint: ptr(true)}}, t.newWordPress)
	mcp.AddTool(s, &mcp.Tool{Name: "wp", Title: "Run wp-cli",
		Description: "Runs wp-cli for a site with the given arguments (e.g. [\"plugin\", \"list\", \"--format=json\"]), " +
			"with the site's PHP version. Returns output and exit code. wp-cli commands can change or delete data.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: ptr(true), OpenWorldHint: ptr(true)}}, t.wp)
	return s
}

func ptr[T any](v T) *T { return &v }

type tools struct{ open Open }

// withCore runs fn on a freshly opened core and returns its result as JSON.
func withCore[T any](ctx context.Context, open Open, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
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

func (t tools) status(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.Status, error) { return c.Status(ctx) })
}

func (t tools) listSites(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) ([]core.SiteStatus, error) { return c.ListSites(ctx) })
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
	return withCore(ctx, t.open, func(c *core.Core) (core.SiteLog, error) { return c.SiteLogs(ctx, in.Site, kind, in.Lines) })
}

func (t tools) listPHP(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.PHPList, error) { return c.ListPHP(ctx) })
}

type phpCheckIn struct {
	Site   string `json:"site" jsonschema:"the site's name"`
	Target string `json:"target,omitempty" jsonschema:"PHP version to check against, e.g. 8.4 (default: the newest fully supported)"`
	All    bool   `json:"all,omitempty" jsonschema:"WordPress sites: also check the WordPress core (default: wp-content only)"`
}

func (t tools) phpCheck(ctx context.Context, _ *mcp.CallToolRequest, in phpCheckIn) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.PHPCheckResult, error) { return c.CheckPHP(ctx, in.Site, in.Target, in.All) })
}

func (t tools) listDatabases(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) ([]string, error) { return c.Databases(ctx) })
}

func (t tools) start(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.StartResult, error) { return c.Start(ctx, 0) })
}

func (t tools) stop(ctx context.Context, _ *mcp.CallToolRequest, _ empty) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.StopResult, error) { return c.Stop(ctx) })
}

type addSiteIn struct {
	Name         string            `json:"name" jsonschema:"site name: lowercase letters, digits and dashes"`
	Path         string            `json:"path" jsonschema:"absolute path of the document root"`
	PHP          string            `json:"php,omitempty" jsonschema:"PHP version, e.g. 8.4 (default 8.4)"`
	Domain       string            `json:"domain,omitempty" jsonschema:"local domain under .localhost (or .test/.local once enabled); default <name>.localhost"`
	Aliases      []string          `json:"aliases,omitempty" jsonschema:"extra domains"`
	CreateFolder bool              `json:"create_folder,omitempty" jsonschema:"create the folder (missing or empty) for a new empty site"`
	Database     string            `json:"database,omitempty" jsonschema:"\"new\" for a database with its own user, or an existing database's name"`
	PHPSettings  map[string]string `json:"php_settings,omitempty" jsonschema:"php.ini values of this site: memory_limit, upload_max_filesize, max_execution_time, display_errors"`
	Force        bool              `json:"force,omitempty" jsonschema:"accept a folder without index.php or index.html"`
}

func (t tools) addSite(ctx context.Context, _ *mcp.CallToolRequest, in addSiteIn) (*mcp.CallToolResult, any, error) {
	if !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.AddSiteRequest{
		Name: in.Name, Path: filepath.Clean(in.Path), PHP: in.PHP, Domain: in.Domain, Aliases: in.Aliases,
		CreateFolder: in.CreateFolder, PHPSettings: in.PHPSettings, Force: in.Force,
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
	return withCore(ctx, t.open, func(c *core.Core) (core.SiteResult, error) { return c.AddSite(ctx, req) })
}

type updateSiteIn struct {
	Name    string    `json:"name" jsonschema:"the site's name"`
	PHP     string    `json:"php,omitempty" jsonschema:"new PHP version, e.g. 8.3"`
	Path    string    `json:"path,omitempty" jsonschema:"new document root (absolute)"`
	Domain  string    `json:"domain,omitempty" jsonschema:"new local domain"`
	Aliases *[]string `json:"aliases,omitempty" jsonschema:"replace the extra domains (empty list clears them)"`
	Enabled *bool     `json:"enabled,omitempty" jsonschema:"serve the site (true) or not (false)"`
	Force   bool      `json:"force,omitempty" jsonschema:"accept a folder without index.php or index.html"`
}

func (t tools) updateSite(ctx context.Context, _ *mcp.CallToolRequest, in updateSiteIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.UpdateSiteRequest{Name: in.Name, PHP: in.PHP, Domain: in.Domain, Aliases: in.Aliases, Enabled: in.Enabled, Force: in.Force}
	if in.Path != "" {
		req.Path = filepath.Clean(in.Path)
	}
	return withCore(ctx, t.open, func(c *core.Core) (core.SiteResult, error) { return c.UpdateSite(ctx, req) })
}

type siteIn struct {
	Site string `json:"site" jsonschema:"the site's name"`
}

func (t tools) createDatabase(ctx context.Context, _ *mcp.CallToolRequest, in siteIn) (*mcp.CallToolResult, any, error) {
	return withCore(ctx, t.open, func(c *core.Core) (core.SiteDatabase, error) { return c.CreateSiteDatabase(ctx, in.Site, "") })
}

type wordpressIn struct {
	Name       string `json:"name" jsonschema:"site name: lowercase letters, digits and dashes"`
	Path       string `json:"path,omitempty" jsonschema:"new or empty folder (absolute); default ~/Sites/<name>"`
	PHP        string `json:"php,omitempty" jsonschema:"PHP version (default 8.4)"`
	Title      string `json:"title,omitempty" jsonschema:"site title"`
	AdminUser  string `json:"admin_user,omitempty" jsonschema:"administrator user (default admin)"`
	AdminEmail string `json:"admin_email,omitempty" jsonschema:"administrator email"`
	Domain     string `json:"domain,omitempty" jsonschema:"local domain (default <name>.localhost)"`
}

func (t tools) newWordPress(ctx context.Context, _ *mcp.CallToolRequest, in wordpressIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.WordPressRequest{Name: in.Name, Path: in.Path, PHP: in.PHP, Title: in.Title,
		AdminUser: in.AdminUser, AdminEmail: in.AdminEmail, Domain: in.Domain}
	return withCore(ctx, t.open, func(c *core.Core) (core.WordPressResult, error) { return c.NewWordPress(ctx, req) })
}

type wpIn struct {
	Site string   `json:"site" jsonschema:"the site's name"`
	Args []string `json:"args" jsonschema:"wp-cli arguments, e.g. [\"plugin\", \"list\", \"--format=json\"]"`
}

// wpOut is wp-cli's result.
type wpOut struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
	Errors   string `json:"errors,omitempty"`
}

// wpTimeout bounds one wp-cli run.
const wpTimeout = 10 * time.Minute

func (t tools) wp(ctx context.Context, _ *mcp.CallToolRequest, in wpIn) (*mcp.CallToolResult, any, error) {
	if len(in.Args) == 0 {
		return toolError(errcode.Errorf(errcode.InvalidInput, "args: give a wp-cli command, e.g. [\"core\", \"version\"]")), nil, nil
	}
	for _, a := range in.Args {
		// --prompt reads secrets from stdin, which an agent cannot provide.
		if strings.HasPrefix(a, "--prompt") || a == "shell" {
			return toolError(errcode.Errorf(errcode.Unsupported, fmt.Sprintf("%q needs a terminal: run `covo wp %s …` yourself", a, in.Site))), nil, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, wpTimeout)
	defer cancel()
	return withCore(ctx, t.open, func(c *core.Core) (wpOut, error) {
		var out, errs bytes.Buffer
		code, err := c.RunWP(ctx, in.Site, in.Args, strings.NewReader(""), &out, &errs)
		return wpOut{ExitCode: code, Output: clip(out.String()), Errors: clip(errs.String())}, err
	})
}
