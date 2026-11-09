// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/store"
)

func TestDatabaseURL(t *testing.T) {
	got := databaseURL(SiteDatabase{Engine: EngineMySQL, User: "shop", Password: "p@ss:w/rd#1", Host: "127.0.0.1", Port: 3307, Database: "shop"})
	if want := "mysql://shop:p%40ss%3Aw%2Frd%231@127.0.0.1:3307/shop"; got != want {
		t.Errorf("mysql = %s, want %s", got, want)
	}
	got = databaseURL(SiteDatabase{Engine: EnginePostgres, User: "api", Password: "x", Host: "localhost", Port: 5433, Database: "api"})
	if want := "postgresql://api:x@127.0.0.1:5433/api"; got != want {
		t.Errorf("pgsql = %s, want %s", got, want)
	}
}

func TestCheckEnvName(t *testing.T) {
	for _, ok := range []string{"API_KEY", "_x", "NEXT_PUBLIC_URL", "DATABASE_URL"} {
		if err := checkEnvName(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "1X", "A-B", "A B", "PORT", "BENBOW_APP_RUN", "HOME"} {
		if err := checkEnvName(bad); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("%q accepted: %v", bad, err)
		}
	}
}

func TestAppEnv(t *testing.T) {
	ctx := context.Background()
	c := runCore(t)
	fake := keychain.NewFake()
	c.keychainRun = fake.Run
	c.launchctl = &launchd.Launchctl{Prefix: "dev.benbow", Run: func(context.Context, ...string) (string, error) {
		return "PID\tStatus\tLabel\n", nil
	}}
	project := t.TempDir()
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "shop", Path: project, Command: "npm run dev"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.AddSite(ctx, AddSiteRequest{Name: "api", Path: project, ProxyPort: 3000}); err != nil {
		t.Fatal(err)
	}

	res, err := c.SetAppEnv(ctx, "shop", map[string]string{"API_KEY": "s3cret \"quoted\"", "DATABASE_URL": "mine"}, nil)
	if err != nil || !slices.Equal(res.Names, []string{"API_KEY", "DATABASE_URL"}) || res.Values != nil || res.Restarted {
		t.Fatalf("set = %+v, %v", res, err)
	}
	if !slices.Contains(res.Provided, "APP_URL") {
		t.Errorf("provided = %v", res.Provided)
	}
	if v := fake.Items["shop@dev.benbow.appenv"]; !strings.Contains(v, "s3cret") {
		t.Errorf("keychain item = %q", v)
	}
	shown, err := c.GetAppEnv(ctx, "shop", true)
	if err != nil || shown.Values["API_KEY"] != "s3cret \"quoted\"" {
		t.Fatalf("show = %+v, %v", shown, err)
	}

	// The user's variables come after Benbow's, so theirs win.
	site, _ := c.Store.Site(ctx, "shop")
	env, names, _ := c.appEnvironment(ctx, site)
	if env[len(env)-1] != "DATABASE_URL=mine" || !slices.Contains(names, "API_KEY") {
		t.Errorf("environment = %q", env)
	}
	if !strings.HasPrefix(env[0], "APP_URL=https://shop.localhost") {
		t.Errorf("APP_URL = %q", env[0])
	}

	if res, err = c.SetAppEnv(ctx, "shop", nil, []string{"API_KEY", "DATABASE_URL"}); err != nil || len(res.Names) != 0 {
		t.Fatalf("unset = %+v, %v", res, err)
	}
	if _, ok := fake.Items["shop@dev.benbow.appenv"]; ok {
		t.Error("an empty set of variables is still in the keychain")
	}

	for name, set := range map[string]map[string]string{
		"api": {"A": "1"}, "shop": {"PORT": "1"},
	} {
		if _, err := c.SetAppEnv(ctx, name, set, nil); errcode.Code(err) != errcode.InvalidInput {
			t.Errorf("%s %v: err = %v", name, set, err)
		}
	}
	if err := c.Store.AddSite(ctx, store.Site{Name: "blog", Path: project, PHPMinor: "8.4", Domain: "blog.localhost"}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetAppEnv(ctx, "blog", false); errcode.Code(err) != errcode.InvalidInput {
		t.Errorf("PHP site err = %v", err)
	}

	// Removing the app removes its variables.
	if _, err := c.SetAppEnv(ctx, "shop", map[string]string{"A": "1"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.RemoveSite(ctx, "shop"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fake.Items["shop@dev.benbow.appenv"]; ok {
		t.Error("variables left behind by a removed app")
	}
}
