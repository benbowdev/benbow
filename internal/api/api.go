// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package api serves the core over a local Unix socket: JSON over HTTP, the
// same envelope and error codes as `covo --json`. It is how the app (and
// later the MCP server) talk to the core; there is no TCP port.
//
// Only the user running the daemon may connect: the socket is 0600 in a 0700
// directory, and every connection's peer UID is checked as well.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/envelope"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
	"github.com/getcovo/covo/internal/version"
)

// SocketName is the API socket's file name in the run directory.
const SocketName = core.APISocketName

// maxBody caps request bodies; every request is a few fields.
const maxBody = 1 << 20

// Backend is the part of the core the API exposes.
type Backend interface {
	Status(ctx context.Context) (core.Status, error)
	Start(ctx context.Context, httpsPort int) (core.StartResult, error)
	Stop(ctx context.Context) (core.StopResult, error)
	ListSites(ctx context.Context) ([]core.SiteStatus, error)
	AddSite(ctx context.Context, req core.AddSiteRequest) (core.SiteResult, error)
	UpdateSite(ctx context.Context, req core.UpdateSiteRequest) (core.SiteResult, error)
	RemoveSite(ctx context.Context, name string) (core.SiteResult, error)
	RemoveSiteAndDatabase(ctx context.Context, name string) (core.SiteResult, error)
	Databases(ctx context.Context) ([]string, error)
	GetSiteDatabase(ctx context.Context, name string) (core.SiteDatabase, error)
	CreateSiteDatabaseOn(ctx context.Context, name, db, engine string) (core.SiteDatabase, error)
	LinkSiteDatabaseOn(ctx context.Context, name, db, engine string) (core.SiteDatabase, error)
	PostgresDatabases(ctx context.Context) ([]string, error)
	Postgres(ctx context.Context) (core.PostgresStatus, error)
	EnablePostgres(ctx context.Context, port int) (core.PostgresResult, error)
	DisablePostgres(ctx context.Context) (core.PostgresResult, error)
	PostgresCredentials(ctx context.Context) (core.MySQLCredentials, error)
	Redis(ctx context.Context) (core.RedisStatus, error)
	EnableRedis(ctx context.Context, port int) (core.RedisResult, error)
	DisableRedis(ctx context.Context) (core.RedisResult, error)
	UnlinkSiteDatabase(ctx context.Context, name string, drop bool) (core.SiteDatabase, error)
	NewWordPress(ctx context.Context, req core.WordPressRequest) (core.WordPressResult, error)
	GetSitePHPSettings(ctx context.Context, name string) (core.SitePHPSettings, error)
	SetSitePHPSettings(ctx context.Context, name string, values map[string]string) (core.SitePHPSettings, error)
	NewAdminerLogin(ctx context.Context, site, engine string) (core.AdminerLogin, error)
	RedeemAdminerLogin(token string) (core.AdminerCredentials, error)
	Mail(ctx context.Context) (core.MailStatus, error)
	SetMail(ctx context.Context, on bool) (core.MailResult, error)
	Updates(ctx context.Context) (core.UpdatesStatus, error)
	CheckUpdates(ctx context.Context) (core.UpdatesStatus, error)
	ApplyUpdates(ctx context.Context, names []string) (core.UpdatesStatus, error)
	RollbackUpdate(ctx context.Context, name string) (core.UpdatesStatus, error)
	SiteLogs(ctx context.Context, name, kind string, lines int) (core.SiteLog, error)
	ClearSiteLogs(ctx context.Context, name string) (core.SiteLog, error)
	ListPHP(ctx context.Context) (core.PHPList, error)
	InstallPHP(ctx context.Context, spec string) (core.PHPInstall, error)
	GetPHPSettings(ctx context.Context, minor string) (core.PHPSettings, error)
	SetPHPSettings(ctx context.Context, minor string, values map[string]string) (core.PHPSettingsResult, error)
	Xdebug(ctx context.Context) ([]core.XdebugStatus, error)
	SetXdebug(ctx context.Context, minor string, on bool) (core.XdebugResult, error)
	Domains(ctx context.Context) ([]core.DomainStatus, error)
	EnableDomain(ctx context.Context, zone string, beforePrompt func()) (core.DomainResult, error)
	DisableDomain(ctx context.Context, zone string, beforePrompt func()) (core.DomainResult, error)
	MySQL(ctx context.Context) (core.MySQLStatus, error)
	EnableMySQL(ctx context.Context, port int) (core.MySQLResult, error)
	DisableMySQL(ctx context.Context) (core.MySQLResult, error)
	MySQLCredentials(ctx context.Context) (core.MySQLCredentials, error)
	Trust(ctx context.Context, beforePrompt func()) (core.TrustResult, error)
	Untrust(ctx context.Context) (core.TrustResult, error)
	Import(ctx context.Context, opts core.ImportOptions) (core.ImportPlan, error)
	UndoMigration(ctx context.Context, name string) (store.Migration, error)
	ListGroups(ctx context.Context) ([]core.GroupInfo, error)
	AddGroup(ctx context.Context, name string, parent int64) (core.GroupInfo, error)
	UpdateGroup(ctx context.Context, req core.UpdateGroupRequest) (core.GroupInfo, error)
	RemoveGroup(ctx context.Context, id int64) (core.GroupInfo, error)
	SetGroupEnabled(ctx context.Context, id int64, enabled bool) (core.GroupEnabledResult, error)
	CheckPHP(ctx context.Context, name, target string, all bool) (core.PHPCheckResult, error)
	NewLaravel(ctx context.Context, req core.LaravelRequest) (core.LaravelResult, error)
	RunToolCaptured(ctx context.Context, name, tool string, args []string) (core.ToolResult, error)
	GetPorts(ctx context.Context) (core.PortsStatus, error)
	GetMCPPermissions(ctx context.Context) (core.MCPPermissions, error)
	SetMCPPermission(ctx context.Context, scope string, write bool) (core.MCPPermissions, error)
	MCPActions(ctx context.Context, limit int) ([]store.MCPAction, error)
	ListBlueprints(ctx context.Context) ([]core.BlueprintInfo, error)
	GetBlueprint(ctx context.Context, name string) (core.Blueprint, error)
	SaveBlueprint(ctx context.Context, bp core.Blueprint, overwrite bool) (core.BlueprintInfo, error)
	DeleteBlueprint(ctx context.Context, name string) error
	BlueprintFromSite(ctx context.Context, site, name string) (core.Blueprint, []string, error)
	ApplyBlueprint(ctx context.Context, site, name string, opts core.ApplyOptions) (core.BlueprintResult, error)
	BlueprintSources(ctx context.Context) ([]store.BlueprintSource, error)
	SetGitHubToken(ctx context.Context, token string) error
	HasGitHubToken(ctx context.Context) bool
	ListRemotes(ctx context.Context, site string) ([]store.Remote, error)
	SetRemote(ctx context.Context, r store.Remote, password *string) (store.Remote, error)
	RemoveRemote(ctx context.Context, site, name string) error
	Pull(ctx context.Context, site, remote string, opts core.SyncOptions) (core.SyncResult, error)
	Push(ctx context.Context, site, remote string, opts core.SyncOptions) (core.SyncResult, error)
	ListSnapshots(ctx context.Context, name string) ([]store.Snapshot, error)
	CreateSnapshot(ctx context.Context, name, kind, note string) (store.Snapshot, error)
	RestoreSnapshot(ctx context.Context, name, id string) (core.RestoreResult, error)
	DeleteSnapshot(ctx context.Context, name, id string) error
	SnapshotKeep(ctx context.Context) (int, error)
	SetSnapshotKeep(ctx context.Context, n int) error
	AgentSessions(ctx context.Context, all bool) ([]core.SessionInfo, error)
	AgentSession(ctx context.Context, site string) (core.SessionInfo, error)
	CloseAgentSession(ctx context.Context, site, outcome string) (core.CloseResult, error)
	SessionIdleHours(ctx context.Context) (int, error)
	SetSessionIdleHours(ctx context.Context, h int) error
	Phone(ctx context.Context) (*core.PhoneStatus, error)
	SharePhone(ctx context.Context, name string, minutes int) (core.PhoneStatus, error)
	StopPhone(ctx context.Context) error
	SetPorts(ctx context.Context, https, http int, beforePrompt func()) (core.PortsStatus, error)
}

// Handler routes /v1 requests to b.
func Handler(b Backend) http.Handler {
	mux := http.NewServeMux()
	get := func(pattern string, fn func(ctx context.Context) (any, error)) {
		mux.HandleFunc("GET "+pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := fn(r.Context())
			reply(w, v, err)
		})
	}
	// post reads the body for fn. A client hanging up does not cancel the
	// change half way: the core finishes it (or rolls it back) regardless.
	post := func(method, pattern string, fn func(r *http.Request, body []byte) (any, error)) {
		mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
			if err != nil {
				reply(w, nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read request: %w", err)))
				return
			}
			v, err := fn(r.WithContext(context.WithoutCancel(r.Context())), body)
			reply(w, v, err)
		})
	}

	get("/v1/version", func(context.Context) (any, error) { return version.Get(), nil })
	get("/v1/status", func(ctx context.Context) (any, error) { return b.Status(ctx) })
	post("POST", "/v1/start", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			HTTPSPort int `json:"https_port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.Start(r.Context(), req.HTTPSPort)
	})
	post("POST", "/v1/stop", func(r *http.Request, body []byte) (any, error) {
		if err := decode(body, &struct{}{}); err != nil {
			return nil, err
		}
		return b.Stop(r.Context())
	})

	get("/v1/sites", func(ctx context.Context) (any, error) { return b.ListSites(ctx) })
	post("POST", "/v1/sites", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name         string            `json:"name"`
			Domain       string            `json:"domain"`
			PHP          string            `json:"php"`
			Path         string            `json:"path"`
			Force        bool              `json:"force"`
			CreateFolder bool              `json:"create_folder"`
			Aliases      []string          `json:"aliases"`
			GroupID      int64             `json:"group_id"`
			PHPSettings  map[string]string `json:"php_settings"`
			NewDatabase  bool              `json:"new_database"`
			LinkDatabase string            `json:"link_database"`
			Engine       string            `json:"database_engine"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.AddSite(r.Context(), core.AddSiteRequest{
			Name: req.Name, Domain: req.Domain, PHP: req.PHP, Path: req.Path, Force: req.Force,
			CreateFolder: req.CreateFolder, Aliases: req.Aliases, Group: req.GroupID, PHPSettings: req.PHPSettings,
			NewDatabase: req.NewDatabase, LinkDatabase: req.LinkDatabase, DatabaseEngine: req.Engine,
		})
	})
	post("PATCH", "/v1/sites/{name}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			PHP      string    `json:"php"`
			Path     string    `json:"path"`
			Domain   string    `json:"domain"`
			Enabled  *bool     `json:"enabled"`
			Archived *bool     `json:"archived"`
			Aliases  *[]string `json:"aliases"`
			GroupID  *int64    `json:"group_id"`
			Force    bool      `json:"force"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.UpdateSite(r.Context(), core.UpdateSiteRequest{
			Name: r.PathValue("name"), PHP: req.PHP, Path: req.Path, Domain: req.Domain, Enabled: req.Enabled,
			Archived: req.Archived, Aliases: req.Aliases, Group: req.GroupID, Force: req.Force,
		})
	})
	get("/v1/groups", func(ctx context.Context) (any, error) { return b.ListGroups(ctx) })
	post("POST", "/v1/groups", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name     string `json:"name"`
			ParentID int64  `json:"parent_id"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.AddGroup(r.Context(), req.Name, req.ParentID)
	})
	post("PATCH", "/v1/groups/{id}", func(r *http.Request, body []byte) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, errcode.Errorf(errcode.InvalidInput, "group id must be a number")
		}
		var req struct {
			Name     string `json:"name"`
			ParentID *int64 `json:"parent_id"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.UpdateGroup(r.Context(), core.UpdateGroupRequest{ID: id, Name: req.Name, Parent: req.ParentID})
	})
	post("DELETE", "/v1/groups/{id}", func(r *http.Request, _ []byte) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, errcode.Errorf(errcode.InvalidInput, "group id must be a number")
		}
		return b.RemoveGroup(r.Context(), id)
	})
	post("POST", "/v1/groups/{id}/enabled", func(r *http.Request, body []byte) (any, error) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			return nil, errcode.Errorf(errcode.InvalidInput, "group id must be a number")
		}
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if req.Enabled == nil {
			return nil, errcode.Errorf(errcode.InvalidInput, "enabled is required")
		}
		return b.SetGroupEnabled(r.Context(), id, *req.Enabled)
	})
	mux.HandleFunc("GET /v1/sites/{name}/logs/{kind}", func(w http.ResponseWriter, r *http.Request) {
		lines := 0
		if v := r.URL.Query().Get("lines"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				reply(w, nil, errcode.Errorf(errcode.InvalidInput, "lines must be a number"))
				return
			}
			lines = n
		}
		v, err := b.SiteLogs(r.Context(), r.PathValue("name"), r.PathValue("kind"), lines)
		reply(w, v, err)
	})
	post("POST", "/v1/wordpress", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name        string            `json:"name"`
			Path        string            `json:"path"`
			PHP         string            `json:"php"`
			Title       string            `json:"title"`
			AdminUser   string            `json:"admin_user"`
			AdminEmail  string            `json:"admin_email"`
			Domain      string            `json:"domain"`
			Aliases     []string          `json:"aliases"`
			GroupID     int64             `json:"group_id"`
			PHPSettings map[string]string `json:"php_settings"`
			// Blueprint: a saved blueprint applied after the installation.
			Blueprint     string `json:"blueprint"`
			AcceptChanges bool   `json:"accept_changes"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.NewWordPress(r.Context(), core.WordPressRequest{
			Name: req.Name, Path: req.Path, PHP: req.PHP, Title: req.Title, AdminUser: req.AdminUser, AdminEmail: req.AdminEmail,
			Domain: req.Domain, Aliases: req.Aliases, Group: req.GroupID, PHPSettings: req.PHPSettings,
			Blueprint: req.Blueprint, AcceptChanges: req.AcceptChanges,
		})
	})
	mux.HandleFunc("GET /v1/sites/{name}/php-settings", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.GetSitePHPSettings(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	// Values are strings as in php.ini; "" removes the site's own value.
	post("PATCH", "/v1/sites/{name}/php-settings", func(r *http.Request, body []byte) (any, error) {
		var req map[string]string
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.SetSitePHPSettings(r.Context(), r.PathValue("name"), req)
	})
	// A one-time URL that opens Adminer logged in (as root, or as a site's
	// database user). Only the daemon keeps the token.
	post("POST", "/v1/adminer/login", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Site   string `json:"site"`
			Engine string `json:"engine"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.NewAdminerLogin(r.Context(), req.Site, req.Engine)
	})
	// Called by Covo's Adminer front file over this same socket.
	post("POST", "/v1/adminer/redeem", func(_ *http.Request, body []byte) (any, error) {
		var req struct {
			Token string `json:"token"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.RedeemAdminerLogin(req.Token)
	})
	get("/v1/updates", func(ctx context.Context) (any, error) { return b.Updates(ctx) })
	post("POST", "/v1/updates/check", func(r *http.Request, _ []byte) (any, error) { return b.CheckUpdates(r.Context()) })
	post("POST", "/v1/updates/apply", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Components []string `json:"components"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.ApplyUpdates(r.Context(), req.Components)
	})
	post("POST", "/v1/updates/rollback", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Component string `json:"component"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.RollbackUpdate(r.Context(), req.Component)
	})
	get("/v1/mail", func(ctx context.Context) (any, error) { return b.Mail(ctx) })
	post("POST", "/v1/mail/enable", func(r *http.Request, _ []byte) (any, error) { return b.SetMail(r.Context(), true) })
	post("POST", "/v1/mail/disable", func(r *http.Request, _ []byte) (any, error) { return b.SetMail(r.Context(), false) })
	get("/v1/databases", func(ctx context.Context) (any, error) { return b.Databases(ctx) })
	get("/v1/postgres/databases", func(ctx context.Context) (any, error) { return b.PostgresDatabases(ctx) })
	mux.HandleFunc("GET /v1/sites/{name}/database", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.GetSiteDatabase(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	// {"name": "shop"} creates a database and its user; with "link": true
	// it links an existing one instead.
	post("POST", "/v1/sites/{name}/database", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name   string `json:"name"`
			Link   bool   `json:"link"`
			Engine string `json:"engine"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if req.Link {
			return b.LinkSiteDatabaseOn(r.Context(), r.PathValue("name"), req.Name, req.Engine)
		}
		return b.CreateSiteDatabaseOn(r.Context(), r.PathValue("name"), req.Name, req.Engine)
	})
	// ?drop=true also deletes the database and the site's own user.
	post("DELETE", "/v1/sites/{name}/database", func(r *http.Request, _ []byte) (any, error) {
		return b.UnlinkSiteDatabase(r.Context(), r.PathValue("name"), r.URL.Query().Get("drop") == "true")
	})
	post("DELETE", "/v1/sites/{name}/logs", func(r *http.Request, _ []byte) (any, error) {
		return b.ClearSiteLogs(r.Context(), r.PathValue("name"))
	})
	post("DELETE", "/v1/sites/{name}", func(r *http.Request, _ []byte) (any, error) {
		// ?drop_database=true also deletes the site's database first.
		if r.URL.Query().Get("drop_database") == "true" {
			return b.RemoveSiteAndDatabase(r.Context(), r.PathValue("name"))
		}
		return b.RemoveSite(r.Context(), r.PathValue("name"))
	})

	get("/v1/php", func(ctx context.Context) (any, error) { return b.ListPHP(ctx) })
	post("POST", "/v1/php", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Version string `json:"version"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.InstallPHP(r.Context(), req.Version)
	})
	mux.HandleFunc("GET /v1/php/{minor}/settings", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.GetPHPSettings(r.Context(), r.PathValue("minor"))
		reply(w, v, err)
	})
	post("PATCH", "/v1/php/{minor}/settings", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			MemoryLimit       *string `json:"memory_limit"`
			UploadMaxFilesize *string `json:"upload_max_filesize"`
			MaxExecutionTime  *int    `json:"max_execution_time"`
			DisplayErrors     *bool   `json:"display_errors"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		values := map[string]string{}
		if req.MemoryLimit != nil {
			values["memory_limit"] = *req.MemoryLimit
		}
		if req.UploadMaxFilesize != nil {
			values["upload_max_filesize"] = *req.UploadMaxFilesize
		}
		if req.MaxExecutionTime != nil {
			values["max_execution_time"] = strconv.Itoa(*req.MaxExecutionTime)
		}
		if req.DisplayErrors != nil {
			values["display_errors"] = strconv.FormatBool(*req.DisplayErrors)
		}
		return b.SetPHPSettings(r.Context(), r.PathValue("minor"), values)
	})
	get("/v1/xdebug", func(ctx context.Context) (any, error) { return b.Xdebug(ctx) })
	post("PUT", "/v1/xdebug/{php}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if req.Enabled == nil {
			return nil, errcode.Errorf(errcode.InvalidInput, `missing "enabled"`)
		}
		return b.SetXdebug(r.Context(), r.PathValue("php"), *req.Enabled)
	})

	post("POST", "/v1/laravel", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name        string            `json:"name"`
			Path        string            `json:"path"`
			PHP         string            `json:"php"`
			Database    string            `json:"database"`
			Engine      string            `json:"database_engine"`
			Domain      string            `json:"domain"`
			Aliases     []string          `json:"aliases"`
			GroupID     int64             `json:"group_id"`
			PHPSettings map[string]string `json:"php_settings"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.NewLaravel(r.Context(), core.LaravelRequest{
			Name: req.Name, Path: req.Path, PHP: req.PHP, Database: req.Database, Engine: req.Engine, Domain: req.Domain,
			Aliases: req.Aliases, Group: req.GroupID, PHPSettings: req.PHPSettings,
		})
	})
	// wp-cli, Composer or Artisan without input; output cut at 256 KiB.
	post("POST", "/v1/sites/{name}/run", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Tool string   `json:"tool"`
			Args []string `json:"args"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.RunToolCaptured(r.Context(), r.PathValue("name"), req.Tool, req.Args)
	})
	// Takes minutes on a large site: the client waits.
	post("POST", "/v1/sites/{name}/php-check", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Target string `json:"target"`
			All    bool   `json:"all"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.CheckPHP(r.Context(), r.PathValue("name"), req.Target, req.All)
	})
	mux.HandleFunc("GET /v1/sites/{name}/snapshots", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.ListSnapshots(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	post("POST", "/v1/sites/{name}/snapshots", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Note string `json:"note"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.CreateSnapshot(r.Context(), r.PathValue("name"), core.SnapshotManual, req.Note)
	})
	post("POST", "/v1/sites/{name}/snapshots/{id}/restore", func(r *http.Request, _ []byte) (any, error) {
		return b.RestoreSnapshot(r.Context(), r.PathValue("name"), r.PathValue("id"))
	})
	post("DELETE", "/v1/sites/{name}/snapshots/{id}", func(r *http.Request, _ []byte) (any, error) {
		return struct{}{}, b.DeleteSnapshot(r.Context(), r.PathValue("name"), r.PathValue("id"))
	})
	get("/v1/snapshots/keep", func(ctx context.Context) (any, error) {
		n, err := b.SnapshotKeep(ctx)
		return map[string]int{"keep": n}, err
	})
	post("PUT", "/v1/snapshots/keep", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Keep int `json:"keep"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if err := b.SetSnapshotKeep(r.Context(), req.Keep); err != nil {
			return nil, err
		}
		return map[string]int{"keep": req.Keep}, nil
	})
	mux.HandleFunc("GET /v1/agent/sessions", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.AgentSessions(r.Context(), r.URL.Query().Get("all") == "1")
		reply(w, v, err)
	})
	mux.HandleFunc("GET /v1/sites/{name}/agent-session", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.AgentSession(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	post("POST", "/v1/sites/{name}/agent-session/keep", func(r *http.Request, _ []byte) (any, error) {
		return b.CloseAgentSession(r.Context(), r.PathValue("name"), core.SessionKeep)
	})
	post("POST", "/v1/sites/{name}/agent-session/revert", func(r *http.Request, _ []byte) (any, error) {
		return b.CloseAgentSession(r.Context(), r.PathValue("name"), core.SessionRevert)
	})
	get("/v1/agent/idle", func(ctx context.Context) (any, error) {
		h, err := b.SessionIdleHours(ctx)
		return map[string]int{"hours": h}, err
	})
	post("PUT", "/v1/agent/idle", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Hours int `json:"hours"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if err := b.SetSessionIdleHours(r.Context(), req.Hours); err != nil {
			return nil, err
		}
		return map[string]int{"hours": req.Hours}, nil
	})
	mux.HandleFunc("GET /v1/sites/{name}/remotes", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.ListRemotes(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	// password: omitted keeps it, "" forgets it; kept in the keychain.
	post("PUT", "/v1/sites/{name}/remotes/{remote}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Kind      string   `json:"kind"`
			Transport string   `json:"transport"`
			Host      string   `json:"host"`
			Port      int      `json:"port"`
			User      string   `json:"user"`
			Path      string   `json:"path"`
			URL       string   `json:"url"`
			Webroot   string   `json:"webroot"`
			Paths     []string `json:"paths"`
			Excludes  []string `json:"excludes"`
			Password  *string  `json:"password"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.SetRemote(r.Context(), store.Remote{
			Site: r.PathValue("name"), Name: r.PathValue("remote"), Kind: req.Kind, Transport: req.Transport, Host: req.Host,
			Port: req.Port, User: req.User, Path: req.Path, URL: req.URL, Webroot: req.Webroot, Paths: req.Paths, Excludes: req.Excludes,
		}, req.Password)
	})
	post("DELETE", "/v1/sites/{name}/remotes/{remote}", func(r *http.Request, _ []byte) (any, error) {
		return struct{}{}, b.RemoveRemote(r.Context(), r.PathValue("name"), r.PathValue("remote"))
	})
	for _, op := range []string{"pull", "push"} {
		post("POST", "/v1/sites/{name}/remotes/{remote}/"+op, func(r *http.Request, body []byte) (any, error) {
			var req struct {
				DryRun     bool `json:"dry_run"`
				NoFiles    bool `json:"no_files"`
				NoDatabase bool `json:"no_database"`
			}
			if err := decode(body, &req); err != nil {
				return nil, err
			}
			o := core.SyncOptions{DryRun: req.DryRun, NoFiles: req.NoFiles, NoDatabase: req.NoDatabase}
			if op == "push" {
				return b.Push(r.Context(), r.PathValue("name"), r.PathValue("remote"), o)
			}
			return b.Pull(r.Context(), r.PathValue("name"), r.PathValue("remote"), o)
		})
	}
	get("/v1/mcp/permissions", func(ctx context.Context) (any, error) { return b.GetMCPPermissions(ctx) })
	post("PUT", "/v1/mcp/permissions", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Scope string `json:"scope"`
			Write bool   `json:"write"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.SetMCPPermission(r.Context(), req.Scope, req.Write)
	})
	get("/v1/mcp/actions", func(ctx context.Context) (any, error) { return b.MCPActions(ctx, 200) })
	get("/v1/blueprints", func(ctx context.Context) (any, error) { return b.ListBlueprints(ctx) })
	get("/v1/blueprints/sources", func(ctx context.Context) (any, error) { return b.BlueprintSources(ctx) })
	mux.HandleFunc("GET /v1/blueprints/{name}", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.GetBlueprint(r.Context(), r.PathValue("name"))
		reply(w, v, err)
	})
	// Body: the blueprint's JSON (its name comes from the path). ?overwrite=1
	// replaces an existing one.
	post("PUT", "/v1/blueprints/{name}", func(r *http.Request, body []byte) (any, error) {
		var raw map[string]any
		if err := decode(body, &raw); err != nil {
			return nil, err
		}
		raw["name"] = r.PathValue("name")
		data, _ := json.Marshal(raw)
		bp, err := core.ParseBlueprint(data)
		if err != nil {
			return nil, err
		}
		return b.SaveBlueprint(r.Context(), bp, r.URL.Query().Get("overwrite") == "1")
	})
	post("DELETE", "/v1/blueprints/{name}", func(r *http.Request, _ []byte) (any, error) {
		return struct{}{}, b.DeleteBlueprint(r.Context(), r.PathValue("name"))
	})
	// Saves a WordPress site as blueprint {name}; warnings list what could
	// not be included.
	post("POST", "/v1/sites/{site}/blueprint", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name      string `json:"name"`
			Overwrite bool   `json:"overwrite"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		bp, warnings, err := b.BlueprintFromSite(r.Context(), r.PathValue("site"), req.Name)
		if err != nil {
			return nil, err
		}
		info, err := b.SaveBlueprint(r.Context(), bp, req.Overwrite)
		return map[string]any{"blueprint": info, "warnings": warnings}, err
	})
	post("POST", "/v1/blueprints/{name}/apply", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Site          string `json:"site"`
			DryRun        bool   `json:"dry_run"`
			AcceptChanges bool   `json:"accept_changes"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.ApplyBlueprint(r.Context(), req.Site, r.PathValue("name"), core.ApplyOptions{DryRun: req.DryRun, AcceptChanges: req.AcceptChanges})
	})
	get("/v1/github-token", func(ctx context.Context) (any, error) {
		return map[string]bool{"stored": b.HasGitHubToken(ctx)}, nil
	})
	// token "" removes it; kept in the login keychain.
	post("PUT", "/v1/github-token", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Token string `json:"token"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if err := b.SetGitHubToken(r.Context(), req.Token); err != nil {
			return nil, err
		}
		return map[string]bool{"stored": req.Token != ""}, nil
	})
	get("/v1/phone", func(ctx context.Context) (any, error) { return b.Phone(ctx) })
	// Opens one site on the private network for minutes (default 30).
	post("PUT", "/v1/sites/{name}/phone", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Minutes int `json:"minutes"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.SharePhone(r.Context(), r.PathValue("name"), req.Minutes)
	})
	post("DELETE", "/v1/phone", func(r *http.Request, _ []byte) (any, error) {
		return struct{}{}, b.StopPhone(r.Context())
	})
	get("/v1/ports", func(ctx context.Context) (any, error) { return b.GetPorts(ctx) })
	// 443 and 80 install Covo's helper: macOS shows an administrator dialog.
	post("PUT", "/v1/ports", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			HTTPSPort int `json:"https_port"`
			HTTPPort  int `json:"http_port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.SetPorts(r.Context(), req.HTTPSPort, req.HTTPPort, nil)
	})
	get("/v1/domains", func(ctx context.Context) (any, error) { return b.Domains(ctx) })
	post("POST", "/v1/domains/{zone}/enable", func(r *http.Request, _ []byte) (any, error) {
		return b.EnableDomain(r.Context(), r.PathValue("zone"), nil)
	})
	post("POST", "/v1/domains/{zone}/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisableDomain(r.Context(), r.PathValue("zone"), nil)
	})

	get("/v1/mysql", func(ctx context.Context) (any, error) { return b.MySQL(ctx) })
	post("POST", "/v1/mysql/enable", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Port int `json:"port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.EnableMySQL(r.Context(), req.Port)
	})
	post("POST", "/v1/mysql/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisableMySQL(r.Context())
	})
	get("/v1/mysql/credentials", func(ctx context.Context) (any, error) { return b.MySQLCredentials(ctx) })

	get("/v1/postgres", func(ctx context.Context) (any, error) { return b.Postgres(ctx) })
	post("POST", "/v1/postgres/enable", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Port int `json:"port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.EnablePostgres(r.Context(), req.Port)
	})
	post("POST", "/v1/postgres/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisablePostgres(r.Context())
	})
	get("/v1/postgres/credentials", func(ctx context.Context) (any, error) { return b.PostgresCredentials(ctx) })
	get("/v1/redis", func(ctx context.Context) (any, error) { return b.Redis(ctx) })
	post("POST", "/v1/redis/enable", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Port int `json:"port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.EnableRedis(r.Context(), req.Port)
	})
	post("POST", "/v1/redis/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisableRedis(r.Context())
	})

	post("POST", "/v1/trust", func(r *http.Request, _ []byte) (any, error) { return b.Trust(r.Context(), nil) })
	post("POST", "/v1/untrust", func(r *http.Request, _ []byte) (any, error) { return b.Untrust(r.Context()) })

	// source: mamp, local, herd, valet or xampp. The source's password is
	// used for this request only.
	post("POST", "/v1/import/{source}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			DryRun         bool     `json:"dry_run"`
			Localhost      bool     `json:"localhost"`
			Sites          []string `json:"sites"`
			Migrate        bool     `json:"migrate"`
			Existing       string   `json:"existing"`
			DumpFile       string   `json:"dump_file"`
			SourceUser     string   `json:"source_user"`
			SourcePassword string   `json:"source_password"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.Import(r.Context(), core.ImportOptions{
			Source: r.PathValue("source"), DryRun: req.DryRun, Localhost: req.Localhost, Sites: req.Sites, Migrate: req.Migrate,
			Migration: core.MigrateOptions{Existing: req.Existing, DumpFile: req.DumpFile, SourceUser: req.SourceUser, SourcePassword: req.SourcePassword},
		})
	})
	post("POST", "/v1/sites/{name}/migration/undo", func(r *http.Request, _ []byte) (any, error) {
		return b.UndoMigration(r.Context(), r.PathValue("name"))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, errcode.Errorf(errcode.NotFound, "no such endpoint: "+r.Method+" "+r.URL.Path))
	})
	return mux
}

// decode reads a JSON object strictly; an empty body is an empty object.
func decode(body []byte, v any) error {
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errcode.New(errcode.InvalidInput, fmt.Errorf("invalid request: %w", err))
	}
	if dec.More() {
		return errcode.Errorf(errcode.InvalidInput, "invalid request: trailing data")
	}
	return nil
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(httpStatus(errcode.Code(err)))
		_ = envelope.Write(w, envelope.Error(err))
		return
	}
	_ = envelope.Write(w, envelope.OK(v))
}

// httpStatus maps an error code to an HTTP status. Clients should rely on
// the code in the body; the status is a convenience.
func httpStatus(code string) int {
	switch code {
	case errcode.Forbidden:
		return http.StatusForbidden
	case errcode.InvalidInput, errcode.ForbiddenFolder:
		return http.StatusBadRequest
	case errcode.NotFound:
		return http.StatusNotFound
	case errcode.Conflict, errcode.PortInUse, errcode.ConfirmationRequired, errcode.Locked:
		return http.StatusConflict
	case errcode.Unsupported:
		return http.StatusUnprocessableEntity
	case errcode.Download, errcode.Checksum:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// Listen creates the API socket at path, readable only by the current user.
// A stale socket left by a crashed daemon is replaced; a live one is not.
func Listen(path string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return nil, errcode.Errorf(errcode.Conflict, "another Covo daemon is already listening on "+path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	old := unix.Umask(0o177)
	ln, err := net.Listen("unix", path)
	unix.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return &uidListener{Listener: ln, uid: os.Getuid()}, nil
}

// uidListener drops connections from any other user.
type uidListener struct {
	net.Listener
	uid int
}

func (l *uidListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err //nolint:wrapcheck // http.Server inspects net.Error as is
		}
		if uid, err := peerUID(c); err == nil && uid == l.uid {
			return c, nil
		}
		_ = c.Close()
	}
}

func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a Unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("raw connection: %w", err)
	}
	uid := -1
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credErr = err
			return
		}
		uid = int(cred.Uid)
	})
	if err == nil {
		err = credErr
	}
	return uid, err
}

// Serve answers API requests on ln until ctx is cancelled, then waits up to
// shutdownWait for requests in flight.
func Serve(ctx context.Context, ln net.Listener, b Backend) error {
	srv := &http.Server{
		Handler:           Handler(b),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: installing PHP or importing sites takes minutes.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shut down API: %w", err)
	}
	return nil
}

const shutdownWait = 50 * time.Second

// Client returns an HTTP client that talks to the API socket at path; the
// host part of request URLs is ignored.
func Client(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}
