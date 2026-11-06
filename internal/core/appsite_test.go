// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/store"
)

func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetectApp(t *testing.T) {
	tests := []struct {
		name      string
		files     map[string]string
		framework string
		command   string
		pm        string
	}{
		{"next with pnpm", map[string]string{
			"package.json":   `{"scripts":{"dev":"next dev"},"dependencies":{"next":"16.0.0","react":"19"}}`,
			"pnpm-lock.yaml": "",
		}, FrameworkNext, "pnpm dev", "pnpm"},
		{"vite with npm", map[string]string{
			"package.json":      `{"scripts":{"dev":"vite","build":"vite build"},"devDependencies":{"vite":"7"}}`,
			"package-lock.json": "{}",
		}, FrameworkVite, "npm run dev -- --port {port} --strictPort", "npm"},
		{"astro from packageManager field", map[string]string{
			"package.json": `{"packageManager":"yarn@4.5.0","scripts":{"dev":"astro dev"},"dependencies":{"astro":"5"}}`,
		}, FrameworkAstro, "yarn dev --port {port}", "yarn"},
		{"sveltekit beats vite", map[string]string{
			"package.json": `{"scripts":{"dev":"vite dev"},"devDependencies":{"vite":"7","@sveltejs/kit":"2"}}`,
			"bun.lock":     "",
		}, FrameworkSvelteKit, "bun run dev --port {port} --strictPort", "bun"},
		{"start script only", map[string]string{
			"package.json": `{"scripts":{"start":"node server.js"}}`,
		}, "", "npm run start", "npm"},
		{"no script", map[string]string{"package.json": `{"name":"x"}`}, "", "", "npm"},
		{"django with venv", map[string]string{
			"manage.py": "", ".venv/bin/python": "",
		}, FrameworkDjango, ".venv/bin/python manage.py runserver 127.0.0.1:{port}", ""},
		{"fastapi with uv", map[string]string{
			"pyproject.toml": "[project]\ndependencies = [\"fastapi>=0.115\", \"uvicorn\"]\n",
			"uv.lock":        "",
			"app/main.py":    "from fastapi import FastAPI\n\napi = FastAPI()\n",
		}, FrameworkFastAPI, "uv run uvicorn app.main:api --reload --port {port}", ""},
		{"flask", map[string]string{
			"requirements.txt": "Flask==3.1\n", "app.py": "",
		}, FrameworkFlask, "flask --app app run --debug --port {port}", ""},
		{"rails", map[string]string{"Gemfile": "", "bin/rails": ""}, FrameworkRails, "bin/rails server -p {port}", ""},
		{"nothing", map[string]string{"README.md": ""}, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFiles(t, dir, tt.files)
			d, err := DetectApp(dir)
			if err != nil {
				t.Fatal(err)
			}
			if d.Framework != tt.framework || d.Command != tt.command || d.PackageManager != tt.pm {
				t.Errorf("got framework %q command %q pm %q, want %q %q %q", d.Framework, d.Command, d.PackageManager, tt.framework, tt.command, tt.pm)
			}
			if d.Command == "" && len(d.Notes) == 0 {
				t.Error("no command and no note saying why")
			}
		})
	}
	if _, err := DetectApp("relative"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("relative path err = %v", err)
	}
}

func appCore(t *testing.T) *Core {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "covo.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &Core{Store: st, Layout: paths.Layout{Root: filepath.Join(dir, "home"), Lock: filepath.Join(dir, "covo.lock")}}
}

func TestAddAppSite(t *testing.T) {
	ctx := context.Background()
	c := appCore(t)
	project := t.TempDir()
	writeFiles(t, project, map[string]string{"package.json": `{"scripts":{"dev":"next dev"},"dependencies":{"next":"16"}}`})

	// A started app: no PHP, no index file needed, a free port from 41000.
	res, err := c.AddSite(ctx, AddSiteRequest{Name: "shop", Path: project, Command: " npm run dev "})
	if err != nil {
		t.Fatal(err)
	}
	s := res.Site
	if !s.IsApp() || s.AppMode != store.AppRun || s.Command != "npm run dev" || s.PHPMinor != "" || s.Framework != FrameworkNext {
		t.Fatalf("site = %+v", s)
	}
	if s.AppPort < appPortFrom || s.AppPort >= appPortFrom+appPortSpan {
		t.Errorf("port = %d, want one from %d", s.AppPort, appPortFrom)
	}
	if !strings.HasPrefix(res.URL, "https://shop.localhost:") {
		t.Errorf("url = %q", res.URL)
	}

	// A second app never gets the same port.
	other, err := c.AddSite(ctx, AddSiteRequest{Name: "blog", Path: project, Command: "npm run dev"})
	if err != nil || other.AppPort == s.AppPort {
		t.Fatalf("second app port = %d (first %d), %v", other.AppPort, s.AppPort, err)
	}

	// A proxied app keeps the user's port; another site may not take it.
	api, err := c.AddSite(ctx, AddSiteRequest{Name: "api", Path: project, ProxyPort: 3000, AppHost: AppHostLocalhost})
	if err != nil || api.AppMode != store.AppProxy || api.AppPort != 3000 || api.Command != "" || api.AppHost != "localhost" {
		t.Fatalf("proxied app = %+v, %v", api.Site, err)
	}
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "api2", Path: project, Command: "npm run dev", Port: 3000}); errcode.Code(err) != errcode.Conflict {
		t.Errorf("taken fixed port err = %v", err)
	}

	// A fixed port something listens on is refused.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	if busy >= 1024 {
		if _, err := c.AddSite(ctx, AddSiteRequest{Name: "busy", Path: project, Command: "npm run dev", Port: busy}); errcode.Code(err) != errcode.PortInUse {
			t.Errorf("busy fixed port err = %v", err)
		}
	}

	for _, bad := range []AddSiteRequest{
		{Name: "x1", Path: project, Command: "npm run dev", ProxyPort: 3001},
		{Name: "x2", Path: project, ProxyPort: 80},
		{Name: "x3", Path: project, Command: "npm run dev\nrm -rf ~"},
		{Name: "x4", Path: project, Command: "npm run dev", PHPSettings: map[string]string{"memory_limit": "1G"}},
		{Name: "x5", Path: project, Command: "npm run dev", CreateFolder: true},
		{Name: "x6", Path: project, ProxyPort: 3002, Port: 41500},
		{Name: "x7", Path: project, Command: "npm run dev", AppHost: "example.com"},
	} {
		if _, err := c.AddSite(ctx, bad); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("%s: err = %v, want invalid_input", bad.Name, err)
		}
	}
}

func TestUpdateAppSite(t *testing.T) {
	ctx := context.Background()
	c := appCore(t)
	project := t.TempDir()
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "web", Path: project, ProxyPort: 3000}); err != nil {
		t.Fatal(err)
	}

	// The Host header alone changes nothing else.
	lh := AppHostLocalhost
	if res, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", AppHost: &lh}); err != nil || res.AppMode != store.AppProxy || res.AppPort != 3000 || res.AppHost != lh {
		t.Fatalf("host only = %+v, %v", res.Site, err)
	}
	// Proxy to started: the user's port is left to them, Covo picks one.
	res, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", Command: "pnpm dev"})
	if err != nil || res.AppMode != store.AppRun || res.AppPort == 3000 || res.Command != "pnpm dev" {
		t.Fatalf("to run = %+v, %v", res.Site, err)
	}
	localhost := AppHostLocalhost
	if res, err = c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", Port: 42999, AppHost: &localhost}); err != nil || res.AppPort != 42999 || res.AppHost != "localhost" {
		t.Fatalf("fixed port = %+v, %v", res.Site, err)
	}
	// Back to proxying: the command is dropped.
	if res, err = c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", ProxyPort: 5173}); err != nil || res.AppMode != store.AppProxy || res.Command != "" || res.AppPort != 5173 {
		t.Fatalf("to proxy = %+v, %v", res.Site, err)
	}
	if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", PHP: "8.4"}); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("PHP on an app err = %v", err)
	}
	if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "web", Port: 42000}); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("port on a proxied app err = %v", err)
	}

	// A PHP site cannot be turned into an app in place.
	php := t.TempDir()
	writeFiles(t, php, map[string]string{"index.php": "<?php"})
	if err := c.Store.AddSite(ctx, store.Site{Name: "old", Path: php, PHPMinor: "8.4", Domain: "old.localhost", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "old", Command: "npm run dev"}); errcode.Code(err) != errcode.InvalidInput ||
		!strings.Contains(err.Error(), "remove the site") {
		t.Errorf("PHP to app err = %v", err)
	}
	if got, _ := c.Store.Site(ctx, "old"); got.Kind != store.KindPHP {
		t.Errorf("stored kind = %q, want php by default", got.Kind)
	}
	if err := phpOnly(res.Site, "wp-cli"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("phpOnly(app) = %v", err)
	}
}
