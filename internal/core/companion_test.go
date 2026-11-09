// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getcovo/covo/internal/ca"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/store"
)

func TestCompanion(t *testing.T) {
	ctx := context.Background()
	c := runCore(t)
	c.Executable = "/usr/local/bin/covo"
	project := t.TempDir()
	writeFiles(t, project, map[string]string{
		"artisan": "", "composer.json": `{"require":{"laravel/framework":"^12.0"}}`, "public/index.php": "<?php",
		"package.json": `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"7","laravel-vite-plugin":"2"}}`,
	})
	if err := c.Store.AddSite(ctx, store.Site{Name: "shop", Path: project + "/public", AppRoot: project, Framework: FrameworkLaravel,
		PHPMinor: "8.4", Domain: "shop.localhost", Enabled: true}); err != nil {
		t.Fatal(err)
	}

	cmd := "npm run dev -- --port {port} --strictPort"
	aliases := []string{"www.shop.localhost"}
	res, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "shop", Companion: &cmd, Aliases: &aliases})
	if err != nil {
		t.Fatal(err)
	}
	if res.CompanionCommand != cmd || res.CompanionPort < appPortFrom {
		t.Fatalf("companion = %q :%d", res.CompanionCommand, res.CompanionPort)
	}
	port := res.CompanionPort

	// It runs in the project root, gets its own agent and log.
	procs := siteProcs(res.Site)
	if len(procs) != 1 || !procs[0].Companion || procs[0].dir() != project || procs[0].port() != port {
		t.Fatalf("procs = %+v", procs)
	}
	a := c.appAgent(procs[0], "")
	if a.Label != "dev.getcovo.companion-shop" || strings.Join(a.Args, " ") != "app run shop --companion" || !strings.HasSuffix(a.StdoutLog, "/shop/companion.log") {
		t.Errorf("agent = %+v", a)
	}
	if got := strings.Join(appCommand("/bin/zsh", procs[0]), " "); !strings.Contains(got, "--port "+strconv.Itoa(port)) {
		t.Errorf("command = %s", got)
	}

	// Another site cannot take its port.
	other := t.TempDir()
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "web", Path: other, Command: "npm run dev", Port: port}); errcode.Code(err) != errcode.Conflict {
		t.Errorf("taken companion port err = %v", err)
	}

	// Laravel + Vite: a certificate for the domain and aliases, and the
	// address without the port.
	if _, err := c.ensureCA(); err != nil {
		t.Fatal(err)
	}
	env, names, warnings := c.companionEnvironment(ctx, res.Site)
	if len(warnings) != 0 || !slices.Contains(env, "APP_URL=https://shop.localhost") || len(names) != 3 {
		t.Fatalf("env = %q, warnings %q", env, warnings)
	}
	cert, key := c.companionCertPaths("shop")
	chain, err := os.ReadFile(cert)
	if err != nil || !ca.LeafCurrent(caDir(c.Layout), chain, []string{"shop.localhost", "www.shop.localhost"}, time.Now()) {
		t.Fatalf("certificate: %v", err)
	}
	if fi, err := os.Stat(key); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("key mode = %v, %v", fi.Mode(), err)
	}
	// A second start reuses the certificate.
	if _, _, w := c.companionEnvironment(ctx, res.Site); len(w) != 0 {
		t.Fatal(w)
	}
	if again, _ := os.ReadFile(cert); string(again) != string(chain) {
		t.Error("the certificate was signed again")
	}
	// Without laravel-vite-plugin nothing is added.
	plain := res.Site
	plain.Framework = ""
	if env, _, _ := c.companionEnvironment(ctx, plain); env != nil {
		t.Errorf("env without the plugin = %q", env)
	}

	// Status, restart rules and removal.
	c.launchctl = &launchd.Launchctl{Prefix: "dev.getcovo", Run: func(context.Context, ...string) (string, error) {
		return "PID\tStatus\tLabel\n", nil
	}}
	if st := c.companionStatus(res.Site, nil); st == nil || st.State != AppStopped || st.Port != port {
		t.Errorf("companion status = %+v", st)
	}
	if st := c.appStatus(res.Site, nil); st != nil {
		t.Errorf("a PHP site has an app status: %+v", st)
	}
	none := ""
	if res, err = c.UpdateSite(ctx, UpdateSiteRequest{Name: "shop", Companion: &none}); err != nil || res.CompanionCommand != "" || res.CompanionPort != 0 {
		t.Fatalf("removed = %+v, %v", res.Site, err)
	}
	if _, err := c.RestartApp(ctx, "shop"); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("restart without companion err = %v", err)
	}
	if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "shop", CompanionPort: 42100}); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("port without companion err = %v", err)
	}
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "front", Path: other, Command: "npm run dev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateSite(ctx, UpdateSiteRequest{Name: "front", Companion: &cmd}); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("companion on an app err = %v", err)
	}
	if _, err := c.RemoveSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cert); !os.IsNotExist(err) {
		t.Errorf("certificate left behind: %v", err)
	}
}
