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
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
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

// Instructions tell agents what Covo is and the recommended workflow.
const Instructions = "Covo is a local PHP/WordPress/Laravel development environment on this Mac (like MAMP PRO): sites are folders " +
	"served over HTTPS at local addresses (e.g. https://shop.localhost:8443), each with its own PHP version and database.\n\n" +
	"Recommended workflow:\n" +
	"1. covo_status, then agent_permissions: you can always read; you change a site only where the user allowed it " +
	"(or while a \"Work with an agent\" session gives you the site).\n" +
	"2. Work inside the site's agent session (session_status): Covo saved the site's files and database when the session " +
	"started and the user will keep your changes or undo them all. Make changes with Covo's wp, composer and artisan tools " +
	"(they use the site's PHP); Covo takes a checkpoint before risky ones. Use dry_run first when unsure.\n" +
	"3. Check your work: fetch_page (the page, its redirects and the PHP errors of that very request), site_logs, " +
	"mail_messages and read_mail for mail PHP sent, query_database to read the database.\n" +
	"4. Before saying you are done, call session_diff and show the user what changed (files and database tables), " +
	"and remind them to keep or undo the changes in Covo.\n\n" +
	"Covo never lets agents delete sites, databases or snapshots, push to servers, or keep or undo a session: those stay with the user."

// New returns the MCP server.
func New(open Open) *mcp.Server {
	t := tools{open: open, conn: &conn{}}
	s := mcp.NewServer(&mcp.Implementation{Name: "covo", Title: "Covo", Version: version.Get().Version}, &mcp.ServerOptions{
		Instructions: Instructions,
	})
	// On the agent's first request (initialize, or server/discover in the
	// newer protocol, or anything else), check where it works.
	s.AddReceivingMiddleware(func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			res, err := next(ctx, method, req)
			if ss, ok := req.GetSession().(*mcp.ServerSession); ok {
				t.conn.once.Do(func() { go t.connected(context.WithoutCancel(ctx), ss) })
			}
			return res, err
		}
	})
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
	mcp.AddTool(s, &mcp.Tool{Name: "session_status", Title: "Agent session", Annotations: ro,
		Description: "A site's agent session: when it started, the snapshot of the starting state the user can go back to, " +
			"and the checkpoints taken before risky changes. Only the user can keep or undo a session, in Covo."}, t.sessionStatus)
	mcp.AddTool(s, &mcp.Tool{Name: "session_diff", Title: "What changed", Annotations: ro,
		Description: "What changed since the site's agent session started: files added, modified and deleted (.git and node_modules left out), " +
			"and database tables with rows inserted, updated and deleted. file gives one file's unified diff; secrets in .env, " +
			"wp-config.php and key files are masked. Show this to the user before saying the work is done."}, t.sessionDiff)
	mcp.AddTool(s, &mcp.Tool{Name: "fetch_page", Title: "Fetch a page", Annotations: ro,
		Description: "Requests a page of a Covo site (GET or HEAD; never another address) like a browser and returns the status, main headers, " +
			"redirects, title, the page's text, and the PHP errors, warnings, notices and deprecations that this very request produced. " +
			"Use it to check your work."}, t.fetchPage)
	mcp.AddTool(s, &mcp.Tool{Name: "mail_messages", Title: "Mail sent", Annotations: ro,
		Description: "The newest messages PHP sent, caught by Covo's Mailpit (nothing leaves the Mac); filter by recipient or subject words."}, t.mailMessages)
	mcp.AddTool(s, &mcp.Tool{Name: "read_mail", Title: "Read a mail", Annotations: ro,
		Description: "One caught message (id from mail_messages, or the newest): sender, recipients, text and links, to check forms and notifications."}, t.readMail)
	mcp.AddTool(s, &mcp.Tool{Name: "query_database", Title: "Query the database", Annotations: ro,
		Description: "Runs one read-only SQL statement (SELECT, SHOW, DESCRIBE, EXPLAIN, WITH) on a site's database, as a user that can only read, " +
			"in a read-only transaction: at most 500 rows, 10 seconds. Changes go through wp or artisan."}, t.queryDatabase)
	mcp.AddTool(s, &mcp.Tool{Name: "list_blueprints", Title: "List blueprints", Annotations: ro,
		Description: "Saved WordPress blueprints (themes, plugins, options); name gives one blueprint's JSON."}, t.listBlueprints)
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
	mcp.AddTool(s, &mcp.Tool{Name: "apply_blueprint", Title: "Apply a blueprint", Annotations: network,
		Description: "Installs a saved blueprint's themes, plugins, options and constants into a WordPress site (third-party code; snapshot first). " +
			"Stops if a download changed since Covo first saw it: only the user can accept that. dry_run lists what would be installed."}, t.applyBlueprint)
	mcp.AddTool(s, &mcp.Tool{Name: "save_blueprint", Title: "Save a site as blueprint", Annotations: change,
		Description: "Saves a WordPress site's wordpress.org themes and plugins, permalinks, timezone and must-use plugins as a blueprint. Needs permission for everything."}, t.saveBlueprint)
	mcp.AddTool(s, &mcp.Tool{Name: "pull", Title: "Pull from production", Annotations: network,
		Description: "Copies a remote's (production or staging) database and uploads into the site. dry_run shows what would change."}, t.pull)
	mcp.AddTool(s, &mcp.Tool{Name: "import_sites", Title: "Import from another tool", Annotations: change,
		Description: "Imports sites from mamp, local, herd, valet or xampp (read only); migrate also copies databases. dry_run (default true) changes nothing. Needs permission for everything."}, t.importSites)
	return s
}

func ptr[T any](v T) *T { return &v }

type tools struct {
	open Open
	conn *conn
}

// conn is what Covo knows about the connected agent.
type conn struct {
	once  sync.Once
	mu    sync.Mutex
	agent string
}

func (c *conn) agentName() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.agent
}

// connected runs once the agent is connected: if it works inside a site's
// folder (its roots, or this process's folder), the site's agent session
// opens now, before the agent changes files with its own tools.
func (t tools) connected(ctx context.Context, ss *mcp.ServerSession) {
	var dirs []string
	if p := ss.InitializeParams(); p != nil {
		if p.ClientInfo != nil {
			t.conn.mu.Lock()
			t.conn.agent = p.ClientInfo.Name
			t.conn.mu.Unlock()
		}
		// Roots are deprecated since protocol 2026-07-28 but still sent by
		// older agents; this process's folder is the fallback.
		if p.Capabilities != nil && p.Capabilities.RootsV2 != nil { //nolint:staticcheck // see above
			rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if res, err := ss.ListRoots(rctx, &mcp.ListRootsParams{}); err == nil { //nolint:staticcheck // see above
				for _, r := range res.Roots {
					if u, err := url.Parse(r.URI); err == nil && u.Scheme == "file" {
						dirs = append(dirs, u.Path)
					}
				}
			}
			cancel()
		}
	}
	if len(dirs) == 0 {
		if wd, err := os.Getwd(); err == nil {
			dirs = append(dirs, wd)
		}
	}
	c, err := t.open(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "covo: %v\n", err)
		return
	}
	defer func() { _ = c.Close() }()
	for _, d := range dirs {
		site, err := c.SiteForFolder(ctx, d)
		if err != nil || site == "" {
			continue
		}
		if _, _, err := c.EnsureAgentSession(ctx, core.SessionRequest{Site: site, Agent: t.conn.agentName(), Source: core.SessionFromFolder}); err != nil {
			// Stderr ends up in the agent's MCP log.
			fmt.Fprintf(os.Stderr, "covo: agent session on %s: %v\n", site, err)
		}
	}
}

// touch notes that the agent works on site: an open session stays active;
// with permission to change the site and no session yet, one opens.
func (t tools) touch(ctx context.Context, c *core.Core, site string) {
	if site == "" {
		return
	}
	if _, err := c.Store.Site(ctx, site); err != nil {
		return
	}
	if _, err := c.Store.OpenAgentSession(ctx, site); err == nil {
		c.TouchAgentSession(ctx, site)
		return
	}
	if c.CheckMCPWrite(ctx, site) == nil {
		_, _, _ = c.EnsureAgentSession(ctx, core.SessionRequest{Site: site, Agent: t.conn.agentName(), Source: core.SessionFromCall})
	}
}

// read runs fn on a freshly opened core and returns its result as JSON.
func read[T any](ctx context.Context, open Open, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
	return readSite(ctx, tools{open: open, conn: &conn{}}, "", fn)
}

// readSite is read for a tool about one site: the site's agent session is
// touched (or opened) first.
func readSite[T any](ctx context.Context, t tools, site string, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
	c, err := t.open(ctx)
	if err != nil {
		return toolError(err), nil, nil
	}
	defer func() { _ = c.Close() }()
	t.touch(ctx, c, site)
	v, err := fn(c)
	if err != nil {
		return toolError(err), nil, nil
	}
	return jsonResult(v), nil, nil
}

// change runs a change for an agent: the user's permission for site ("":
// everything), the site's agent session (opened if needed, with its
// starting snapshot), a checkpoint first if the change is risky (unless
// dryRun, or the site does not exist yet, or ownSnapshot: the tool takes
// its own), the change, and a log entry.
func change[T any](ctx context.Context, t tools, tool, site string, args any, dryRun, ownSnapshot bool, fn func(*core.Core) (T, error)) (*mcp.CallToolResult, any, error) {
	c, err := t.open(ctx)
	if err != nil {
		return toolError(err), nil, nil
	}
	defer func() { _ = c.Close() }()
	argJSON, _ := json.Marshal(args)
	var v T
	res, err := c.RunAgentChange(ctx, core.AgentChange{
		Tool: tool, Site: site, Args: string(argJSON), Agent: t.conn.agentName(),
		DryRun: dryRun, Risky: !ownSnapshot && riskyArgs(tool, args),
	}, func() error {
		var err error
		v, err = fn(c)
		return err
	})
	if err != nil {
		return toolError(err), nil, nil
	}
	out := map[string]any{"result": v}
	if res.Checkpoint != "" {
		out["checkpoint"] = res.Checkpoint
		out["undo_this"] = "restore_snapshot with id " + res.Checkpoint
	}
	if res.SessionStart != "" {
		out["session_start"] = res.SessionStart
		out["undo_all"] = "the user can keep or undo everything since the session started in Covo"
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
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.SiteLog, error) { return c.SiteLogs(ctx, in.Site, kind, in.Lines) })
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
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.PHPCheckResult, error) { return c.CheckPHP(ctx, in.Site, in.Target, in.All) })
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
	return readSite(ctx, t, in.Site, func(c *core.Core) ([]store.Snapshot, error) { return c.ListSnapshots(ctx, in.Site) })
}

type fetchIn struct {
	Site   string `json:"site" jsonschema:"the site's name"`
	Path   string `json:"path,omitempty" jsonschema:"path on the site, e.g. /shop/?p=2 (default /), or a full URL on a Covo site's address"`
	Method string `json:"method,omitempty" jsonschema:"GET (default) or HEAD"`
	HTML   bool   `json:"html,omitempty" jsonschema:"also return the page's HTML (cut at 100 KiB)"`
}

func (t tools) fetchPage(ctx context.Context, _ *mcp.CallToolRequest, in fetchIn) (*mcp.CallToolResult, any, error) {
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.FetchResult, error) {
		return c.FetchPage(ctx, core.FetchRequest{Site: in.Site, Path: in.Path, Method: in.Method, HTML: in.HTML})
	})
}

type mailIn struct {
	To      string `json:"to,omitempty" jsonschema:"only mail to this address"`
	Subject string `json:"subject,omitempty" jsonschema:"only mail with these words in the subject"`
	Limit   int    `json:"limit,omitempty" jsonschema:"how many (default 20)"`
}

func (t tools) mailMessages(ctx context.Context, _ *mcp.CallToolRequest, in mailIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) ([]core.MailMessage, error) {
		return c.MailMessages(ctx, core.MailQuery{To: in.To, Subject: in.Subject, Limit: in.Limit})
	})
}

type readMailIn struct {
	ID string `json:"id,omitempty" jsonschema:"the message id from mail_messages (default: the newest)"`
}

func (t tools) readMail(ctx context.Context, _ *mcp.CallToolRequest, in readMailIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (core.MailContent, error) { return c.MailMessage(ctx, in.ID) })
}

type queryIn struct {
	Site string `json:"site" jsonschema:"the site's name"`
	SQL  string `json:"sql" jsonschema:"one SELECT, SHOW, DESCRIBE, EXPLAIN or WITH statement"`
}

func (t tools) queryDatabase(ctx context.Context, _ *mcp.CallToolRequest, in queryIn) (*mcp.CallToolResult, any, error) {
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.QueryResult, error) { return c.QueryDatabase(ctx, in.Site, in.SQL) })
}

type diffIn struct {
	Site string `json:"site" jsonschema:"the site's name"`
	File string `json:"file,omitempty" jsonschema:"one file's unified diff (path inside the project folder); without it, the list of changes"`
}

func (t tools) sessionDiff(ctx context.Context, _ *mcp.CallToolRequest, in diffIn) (*mcp.CallToolResult, any, error) {
	if in.File != "" {
		return readSite(ctx, t, in.Site, func(c *core.Core) (core.FileDiffResult, error) {
			return c.AgentSessionFileDiff(ctx, in.Site, in.File, core.DiffOptions{Mask: true})
		})
	}
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.SessionDiff, error) { return c.AgentSessionDiff(ctx, in.Site) })
}

func (t tools) sessionStatus(ctx context.Context, _ *mcp.CallToolRequest, in siteIn) (*mcp.CallToolResult, any, error) {
	return readSite(ctx, t, in.Site, func(c *core.Core) (core.SessionInfo, error) { return c.AgentSession(ctx, in.Site) })
}

func (t tools) start(ctx context.Context, _ *mcp.CallToolRequest, in dryIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t, "start", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
		if in.DryRun {
			return "would start every enabled site", nil
		}
		return c.Start(ctx, 0)
	})
}

func (t tools) stop(ctx context.Context, _ *mcp.CallToolRequest, in dryIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t, "stop", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "add_site", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "update_site", in.Name, in, in.DryRun, false, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "create_site_database", in.Site, in, in.DryRun, true, func(c *core.Core) (any, error) {
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
	Blueprint  string `json:"blueprint,omitempty" jsonschema:"saved blueprint to apply (list_blueprints)"`
	DryRun     bool   `json:"dry_run,omitempty" jsonschema:"only say what would happen"`
}

func (t tools) newWordPress(ctx context.Context, _ *mcp.CallToolRequest, in wordpressIn) (*mcp.CallToolResult, any, error) {
	if in.Path != "" && !filepath.IsAbs(in.Path) {
		return toolError(errcode.Errorf(errcode.InvalidInput, "path must be absolute")), nil, nil
	}
	req := core.WordPressRequest{Name: in.Name, Path: in.Path, PHP: in.PHP, Title: in.Title,
		AdminUser: in.AdminUser, AdminEmail: in.AdminEmail, Domain: in.Domain, Blueprint: in.Blueprint}
	return change(ctx, t, "new_wordpress", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "new_laravel", "", in, in.DryRun, false, func(c *core.Core) (any, error) {
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
				return toolError(errcode.Newf(errcode.Unsupported, "%q needs a terminal: run `covo %s %s …` yourself", a, tool, in.Site)), nil, nil
			}
		}
		ctx, cancel := context.WithTimeout(ctx, toolTimeout)
		defer cancel()
		return change(ctx, t, tool, in.Site, in, in.DryRun, false, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "create_snapshot", in.Site, in, false, true, func(c *core.Core) (store.Snapshot, error) {
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
	return change(ctx, t, "restore_snapshot", in.Site, in, in.DryRun, true, func(c *core.Core) (any, error) {
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
	return change(ctx, t, "pull", in.Site, in, in.DryRun, true, func(c *core.Core) (core.SyncResult, error) {
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
	return change(ctx, t, "import_sites", "", in, dry, false, func(c *core.Core) (core.ImportPlan, error) {
		return c.Import(ctx, core.ImportOptions{Source: in.Source, Sites: in.Sites, Localhost: in.Localhost, Migrate: in.Migrate, DryRun: dry})
	})
}

type blueprintsIn struct {
	Name string `json:"name,omitempty" jsonschema:"a blueprint's name, to get its JSON"`
}

func (t tools) listBlueprints(ctx context.Context, _ *mcp.CallToolRequest, in blueprintsIn) (*mcp.CallToolResult, any, error) {
	return read(ctx, t.open, func(c *core.Core) (any, error) {
		if in.Name != "" {
			return c.GetBlueprint(ctx, in.Name)
		}
		return c.ListBlueprints(ctx)
	})
}

type applyBlueprintIn struct {
	Blueprint string `json:"blueprint" jsonschema:"the blueprint's name"`
	Site      string `json:"site" jsonschema:"a WordPress site"`
	DryRun    bool   `json:"dry_run,omitempty" jsonschema:"only list what would be installed"`
}

func (t tools) applyBlueprint(ctx context.Context, _ *mcp.CallToolRequest, in applyBlueprintIn) (*mcp.CallToolResult, any, error) {
	// ApplyBlueprint takes its own snapshot.
	return change(ctx, t, "apply_blueprint", in.Site, in, in.DryRun, true, func(c *core.Core) (core.BlueprintResult, error) {
		return c.ApplyBlueprint(ctx, in.Site, in.Blueprint, core.ApplyOptions{DryRun: in.DryRun})
	})
}

type saveBlueprintIn struct {
	Site      string `json:"site" jsonschema:"a WordPress site"`
	Name      string `json:"name" jsonschema:"the blueprint's name: lowercase letters, digits and dashes"`
	Overwrite bool   `json:"overwrite,omitempty" jsonschema:"replace a blueprint with the same name"`
}

func (t tools) saveBlueprint(ctx context.Context, _ *mcp.CallToolRequest, in saveBlueprintIn) (*mcp.CallToolResult, any, error) {
	return change(ctx, t, "save_blueprint", "", in, false, true, func(c *core.Core) (any, error) {
		bp, warnings, err := c.BlueprintFromSite(ctx, in.Site, in.Name)
		if err != nil {
			return nil, err
		}
		info, err := c.SaveBlueprint(ctx, bp, in.Overwrite)
		return map[string]any{"blueprint": info, "warnings": warnings}, err
	})
}

// riskyArgs reports whether an agent's change deserves a checkpoint inside
// its session (core.RiskyAgentCall, plus a new PHP version or folder).
func riskyArgs(tool string, args any) bool {
	switch in := args.(type) {
	case toolIn:
		return core.RiskyAgentCall(tool, in.Args)
	case updateSiteIn:
		return in.PHP != "" || in.Path != ""
	}
	return false
}
