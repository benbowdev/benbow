// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
)

// fake records calls; methods not set up fail with not_found.
type fake struct {
	Backend
	calls []string
	added core.AddSiteRequest
	err   error
}

func (f *fake) record(call string) error {
	f.calls = append(f.calls, call)
	return f.err
}

func (f *fake) Status(context.Context) (core.Status, error) {
	return core.Status{Running: true, HTTPSPort: 8443}, f.record("status")
}

func (f *fake) Start(_ context.Context, port int) (core.StartResult, error) {
	return core.StartResult{HTTPSPort: port}, f.record("start")
}

func (f *fake) AddSite(_ context.Context, req core.AddSiteRequest) (core.SiteResult, error) {
	f.added = req
	return core.SiteResult{}, f.record("add")
}

func (f *fake) UpdateSite(_ context.Context, req core.UpdateSiteRequest) (core.SiteResult, error) {
	call := "update " + req.Name + " " + req.PHP + " " + req.Domain
	if req.Enabled != nil {
		call += map[bool]string{true: " on", false: " off"}[*req.Enabled]
	}
	if req.Aliases != nil {
		call += " aliases=" + strings.Join(*req.Aliases, ",")
	}
	return core.SiteResult{}, f.record(call)
}

func (f *fake) SetPHPSettings(_ context.Context, minor string, values map[string]string) (core.PHPSettingsResult, error) {
	return core.PHPSettingsResult{}, f.record("php settings " + minor + " " + values["memory_limit"] + " " + values["display_errors"])
}

func (f *fake) SiteLogs(_ context.Context, name, kind string, lines int) (core.SiteLog, error) {
	return core.SiteLog{}, f.record(fmt.Sprintf("logs %s %s %d", name, kind, lines))
}

func (f *fake) ClearSiteLogs(_ context.Context, name string) (core.SiteLog, error) {
	return core.SiteLog{}, f.record("clear logs " + name)
}

func (f *fake) CreateSiteDatabase(_ context.Context, name, db string) (core.SiteDatabase, error) {
	return core.SiteDatabase{}, f.record("create db " + name + " " + db)
}

func (f *fake) LinkSiteDatabase(_ context.Context, name, db string) (core.SiteDatabase, error) {
	return core.SiteDatabase{}, f.record("link db " + name + " " + db)
}

func (f *fake) UnlinkSiteDatabase(_ context.Context, name string, drop bool) (core.SiteDatabase, error) {
	return core.SiteDatabase{}, f.record(fmt.Sprintf("unlink db %s %v", name, drop))
}

func (f *fake) NewWordPress(_ context.Context, req core.WordPressRequest) (core.WordPressResult, error) {
	return core.WordPressResult{}, f.record("wordpress " + req.Name + " " + req.PHP + " " + req.AdminUser)
}

func (f *fake) SetSitePHPSettings(_ context.Context, name string, values map[string]string) (core.SitePHPSettings, error) {
	return core.SitePHPSettings{}, f.record(fmt.Sprintf("site php %s %q", name, values["memory_limit"]))
}

func (f *fake) NewAdminerLogin(_ context.Context, site string) (core.AdminerLogin, error) {
	return core.AdminerLogin{}, f.record("adminer login " + site)
}

func (f *fake) RedeemAdminerLogin(token string) (core.AdminerCredentials, error) {
	return core.AdminerCredentials{}, f.record("adminer redeem " + token)
}

func (f *fake) RemoveSite(_ context.Context, name string) (core.SiteResult, error) {
	return core.SiteResult{}, f.record("remove " + name)
}

func (f *fake) SetXdebug(_ context.Context, minor string, on bool) (core.XdebugResult, error) {
	return core.XdebugResult{}, f.record("xdebug " + minor + " " + map[bool]string{true: "on", false: "off"}[on])
}

func (f *fake) EnableDomain(_ context.Context, zone string, _ func()) (core.DomainResult, error) {
	return core.DomainResult{}, f.record("enable " + zone)
}

func (f *fake) ImportMAMP(_ context.Context, opts core.ImportOptions) (core.ImportPlan, error) {
	return core.ImportPlan{DryRun: opts.DryRun}, f.record("import " + map[bool]string{true: "dry", false: "real"}[opts.DryRun])
}

type response struct {
	Schema int             `json:"schema"`
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Error  *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// serve starts the API on a real socket and returns a client for it.
func serve(t *testing.T, b Backend) *http.Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "covo-api-") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(sock); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode: %v %v", fi.Mode(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, b) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	return Client(sock)
}

func call(t *testing.T, c *http.Client, method, path, body string) (int, response) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, "http://covo"+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var r response
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		t.Fatalf("%s %s: body is not JSON: %v", method, path, err)
	}
	if r.Schema != 1 {
		t.Fatalf("schema = %d", r.Schema)
	}
	return resp.StatusCode, r
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		method, path, body string
		wantStatus         int
		wantCall           string
		wantCode           string
	}{
		{"GET", "/v1/version", "", 200, "", ""},
		{"GET", "/v1/status", "", 200, "status", ""},
		{"POST", "/v1/start", `{"https_port":9443}`, 200, "start", ""},
		{"POST", "/v1/start", "", 200, "start", ""},
		{"POST", "/v1/sites", `{"name":"demo","php":"8.4","path":"/Users/x/demo","force":true}`, 200, "add", ""},
		{"DELETE", "/v1/sites/demo", "", 200, "remove demo", ""},
		{"PATCH", "/v1/sites/demo", `{"php":"8.2","domain":"demo.test"}`, 200, "update demo 8.2 demo.test", ""},
		{"PATCH", "/v1/sites/demo", `{"enabled":false}`, 200, "update demo   off", ""},
		{"PATCH", "/v1/php/8.4/settings", `{"memory_limit":"1G","display_errors":false}`, 200, "php settings 8.4 1G false", ""},
		{"PATCH", "/v1/php/8.4/settings", `{"short_open_tag":"On"}`, 400, "", errcode.InvalidInput},
		{"PATCH", "/v1/sites/demo", `{"aliases":["www.demo.localhost"]}`, 200, "update demo   aliases=www.demo.localhost", ""},
		{"PATCH", "/v1/sites/demo", `{"aliases":[]}`, 200, "update demo   aliases=", ""},
		{"GET", "/v1/sites/demo/logs/php?lines=20", "", 200, "logs demo php 20", ""},
		{"GET", "/v1/sites/demo/logs/access", "", 200, "logs demo access 0", ""},
		{"GET", "/v1/sites/demo/logs/php?lines=x", "", 400, "", errcode.InvalidInput},
		{"DELETE", "/v1/sites/demo/logs", "", 200, "clear logs demo", ""},
		{"POST", "/v1/sites/demo/database", `{}`, 200, "create db demo ", ""},
		{"POST", "/v1/sites/demo/database", `{"name":"shop","link":true}`, 200, "link db demo shop", ""},
		{"DELETE", "/v1/sites/demo/database", "", 200, "unlink db demo false", ""},
		{"DELETE", "/v1/sites/demo/database?drop=true", "", 200, "unlink db demo true", ""},
		{"POST", "/v1/wordpress", `{"name":"blog","php":"8.4","admin_user":"me"}`, 200, "wordpress blog 8.4 me", ""},
		{"PATCH", "/v1/sites/demo/php-settings", `{"memory_limit":""}`, 200, `site php demo ""`, ""},
		{"PATCH", "/v1/sites/demo/php-settings", `{"memory_limit":1}`, 400, "", errcode.InvalidInput},
		{"POST", "/v1/adminer/login", `{"site":"demo"}`, 200, "adminer login demo", ""},
		{"POST", "/v1/adminer/redeem", `{"token":"abc"}`, 200, "adminer redeem abc", ""},
		{"PATCH", "/v1/sites/demo", `{"name":"x"}`, 400, "", errcode.InvalidInput},
		{"PUT", "/v1/xdebug/8.4", `{"enabled":true}`, 200, "xdebug 8.4 on", ""},
		{"PUT", "/v1/xdebug/8.4", `{"enabled":false}`, 200, "xdebug 8.4 off", ""},
		{"PUT", "/v1/xdebug/8.4", `{}`, 400, "", errcode.InvalidInput},
		{"POST", "/v1/domains/local/enable", "", 200, "enable local", ""},
		{"POST", "/v1/import/mamp", `{"dry_run":true}`, 200, "import dry", ""},
		{"POST", "/v1/sites", `{"name":"demo","bogus":1}`, 400, "", errcode.InvalidInput},
		{"POST", "/v1/sites", `{"name":"demo"} {}`, 400, "", errcode.InvalidInput},
		{"POST", "/v1/sites", `not json`, 400, "", errcode.InvalidInput},
		{"GET", "/v1/nope", "", 404, "", errcode.NotFound},
		{"DELETE", "/v1/status", "", 404, "", errcode.NotFound},
	}
	f := &fake{}
	c := serve(t, f)
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path+" "+tt.body, func(t *testing.T) {
			f.calls = nil
			status, r := call(t, c, tt.method, tt.path, tt.body)
			if status != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%+v)", status, tt.wantStatus, r.Error)
			}
			if r.OK != (tt.wantCode == "") {
				t.Fatalf("ok = %v", r.OK)
			}
			if tt.wantCode != "" && (r.Error == nil || r.Error.Code != tt.wantCode) {
				t.Fatalf("error = %+v, want %s", r.Error, tt.wantCode)
			}
			if tt.wantCall != "" && (len(f.calls) != 1 || f.calls[0] != tt.wantCall) {
				t.Fatalf("calls = %v, want %q", f.calls, tt.wantCall)
			}
			if tt.wantCall == "" && len(f.calls) != 0 {
				t.Fatalf("unexpected calls %v", f.calls)
			}
		})
	}
	if want := (core.AddSiteRequest{Name: "demo", PHP: "8.4", Path: "/Users/x/demo", Force: true}); f.added != want {
		t.Fatalf("add request = %+v", f.added)
	}
}

func TestErrorCodes(t *testing.T) {
	for code, status := range map[string]int{
		errcode.InvalidInput:         400,
		errcode.ForbiddenFolder:      400,
		errcode.NotFound:             404,
		errcode.PortInUse:            409,
		errcode.ConfirmationRequired: 409,
		errcode.Unsupported:          422,
		errcode.Download:             502,
		errcode.Internal:             500,
	} {
		t.Run(code, func(t *testing.T) {
			c := serve(t, &fake{err: errcode.Errorf(code, "boom")})
			got, r := call(t, c, "GET", "/v1/status", "")
			if got != status || r.OK || r.Error.Code != code || r.Error.Message != "boom" {
				t.Fatalf("status %d, %+v", got, r.Error)
			}
		})
	}
}

func TestListenRefusesLiveSocket(t *testing.T) {
	dir, err := os.MkdirTemp("", "covo-api-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, SocketName)

	// A stale socket (nobody listening) is replaced.
	stale, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	defer ln.Close()

	// A live one is not.
	if _, err := Listen(sock); errcode.Code(err) != errcode.Conflict {
		t.Fatalf("live socket: %v", err)
	}
}

func TestOtherUserIsDropped(t *testing.T) {
	dir, err := os.MkdirTemp("", "covo-api-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, SocketName)
	ln, err := Listen(sock)
	if err != nil {
		t.Fatal(err)
	}
	// Pretend the daemon runs as someone else: this test's process is then
	// "another user" and must get nothing back.
	ln.(*uidListener).uid = os.Getuid() + 1
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, ln, &fake{}) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()

	req, _ := http.NewRequestWithContext(context.Background(), "GET", "http://covo/v1/version", nil)
	resp, err := Client(sock).Do(req)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("another user got an answer: %s", resp.Status)
	}
}

func TestClientDisconnectDoesNotCancel(t *testing.T) {
	b := &slowAdd{started: make(chan struct{}), finished: make(chan error, 1)}
	c := serve(t, b)
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://covo/v1/sites", bytes.NewReader([]byte(`{"name":"x"}`)))
	go func() {
		<-b.started
		cancel()
	}()
	if resp, err := c.Do(req); err == nil {
		resp.Body.Close()
	}
	if err := <-b.finished; err != nil {
		t.Fatalf("the change was cancelled: %v", err)
	}
}

type slowAdd struct {
	fake
	started  chan struct{}
	finished chan error
}

func (s *slowAdd) AddSite(ctx context.Context, _ core.AddSiteRequest) (core.SiteResult, error) {
	close(s.started)
	// Give the hang-up time to propagate.
	select {
	case <-ctx.Done():
		s.finished <- ctx.Err()
	case <-timeAfter():
		s.finished <- nil
	}
	return core.SiteResult{}, nil
}

func timeAfter() <-chan time.Time { return time.After(300 * time.Millisecond) }
