// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiffTrees(t *testing.T) {
	before, after := t.TempDir(), t.TempDir()
	write := func(root, p, body string, at time.Time) {
		full := filepath.Join(root, p)
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(full, at, at)
	}
	t0, t1 := time.Now().Add(-time.Hour), time.Now()
	write(before, "same.php", "a", t0)
	write(after, "same.php", "a", t0)
	write(before, "touched.php", "abc", t0)
	write(after, "touched.php", "abc", t1) // same content, new time
	write(before, "edited.php", "abc", t0)
	write(after, "edited.php", "abd", t1) // same size, new content
	write(before, "grown.php", "a", t0)
	write(after, "grown.php", "ab", t0)
	write(before, "gone.php", "x", t0)
	write(after, "wp-content/new.php", "x", t1)
	write(after, ".env", "APP_KEY=x", t1)
	write(after, ".git/HEAD", "x", t1)
	write(after, "theme/node_modules/x.js", "x", t1)

	d, err := diffTrees(before, after)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range d.Changes {
		got[c.Path] = c.Change
	}
	want := map[string]string{"edited.php": ChangeModified, "grown.php": ChangeModified, "gone.php": ChangeDeleted,
		"wp-content/new.php": ChangeAdded, ".env": ChangeAdded}
	if len(got) != len(want) || d.Added != 2 || d.Modified != 2 || d.Deleted != 1 {
		t.Fatalf("changes = %v (+%d ~%d -%d)", got, d.Added, d.Modified, d.Deleted)
	}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("%s = %q, want %q", p, got[p], c)
		}
	}
	for _, c := range d.Changes {
		if c.Path == ".env" && !c.Sensitive {
			t.Error(".env not marked sensitive")
		}
	}
}

func TestMaskSecrets(t *testing.T) {
	tests := []struct {
		path, in, want string
	}{
		{"wp-config.php", "+define( 'DB_PASSWORD', 'hunter2' );", "+define( 'DB_PASSWORD', '•••' );"},
		{"wp-config.php", `-define("AUTH_KEY", "a'b;c");`, `-define("AUTH_KEY", "•••");`},
		{"wp-config.php", "+define( 'DB_NAME', 'shop' );", "+define( 'DB_NAME', 'shop' );"},
		{"wp-config.php", "+$db_password = 'x';", "+$db_password = '•••';"},
		{".env", "+DB_PASSWORD=secret", "+DB_PASSWORD=•••"},
		{".env", " APP_KEY=base64:abc", " APP_KEY=•••"},
		{".env.local", "-export STRIPE_SECRET=\"sk_live\"", "-export STRIPE_SECRET=•••"},
		{".env", "+APP_URL=https://shop.localhost", "+APP_URL=https://shop.localhost"},
		{"auth.json", `+  "github-oauth": {"github.com": "x"}, "password": "p"`, `+  "github-oauth": {"github.com": "x"}, "password": "•••"`},
		{"ssl/site.key", "+MIIEvQIBADAN\n-ABC", "+•••\n-•••"},
	}
	for _, tt := range tests {
		if got := MaskSecrets(tt.path, tt.in); got != tt.want {
			t.Errorf("MaskSecrets(%s, %q) = %q, want %q", tt.path, tt.in, got, tt.want)
		}
	}
}

func TestCompareDBStats(t *testing.T) {
	before := dbStats{Engine: "mysql", Database: "shop", ServerStart: "s1", Tables: map[string]tableStat{
		"wp_options": {Inserted: 10, Updated: 5, Schema: "a", Rows: 100},
		"wp_posts":   {Inserted: 3, Schema: "b", Rows: 50},
		"wp_old":     {Schema: "c", Rows: 7},
		"wp_users":   {Schema: "d", Rows: 1},
	}}
	after := dbStats{Engine: "mysql", Database: "shop", ServerStart: "s1", Tables: map[string]tableStat{
		"wp_options": {Inserted: 12, Updated: 9, Deleted: 1, Schema: "a", Rows: 101},
		"wp_posts":   {Inserted: 3, Schema: "b2", Rows: 50},
		"wp_new":     {Inserted: 4, Schema: "e", Rows: 4},
		"wp_users":   {Schema: "d", Rows: 1},
	}}
	d := compareDBStats(before, after)
	got := map[string]TableChange{}
	for _, tc := range d.Tables {
		got[tc.Table] = tc
	}
	if d.Approximate || len(got) != 4 {
		t.Fatalf("diff = %+v", d)
	}
	if o := got["wp_options"]; o.Change != "rows" || o.Inserted != 2 || o.Updated != 4 || o.Deleted != 1 {
		t.Errorf("wp_options = %+v", o)
	}
	if got["wp_posts"].Change != "altered" || got["wp_new"].Change != "added" || got["wp_old"].Change != "dropped" {
		t.Errorf("tables = %+v", got)
	}
	after.ServerStart = "s2"
	if d := compareDBStats(before, after); !d.Approximate || d.Note == "" {
		t.Errorf("restart not flagged: %+v", d)
	}
	for _, tt := range []struct {
		a, b string
		same bool
	}{{"1000", "1002", true}, {"1000", "1010", false}, {"", "", false}, {"x", "x", true}} {
		if got := sameServerStart(tt.a, tt.b); got != tt.same {
			t.Errorf("sameServerStart(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestAgentSessionFileDiff(t *testing.T) {
	ctx := context.Background()
	c, site := sessionCore(t)
	_ = os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("<?php\ndefine( 'DB_PASSWORD', 'old' );\n"), 0o644)
	if _, _, err := c.EnsureAgentSession(ctx, SessionRequest{Site: "shop", Source: SessionFromCall}); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(site, "wp-config.php"), []byte("<?php\ndefine( 'DB_PASSWORD', 'new-secret' );\n"), 0o644)
	r, err := c.AgentSessionFileDiff(ctx, "shop", "wp-config.php", DiffOptions{Mask: true})
	if err != nil || !r.Masked || strings.Contains(r.Diff, "new-secret") || strings.Contains(r.Diff, "'old'") || !strings.Contains(r.Diff, "+define") {
		t.Fatalf("masked diff = %+v, %v", r, err)
	}
	if r, _ := c.AgentSessionFileDiff(ctx, "shop", "wp-config.php", DiffOptions{}); !strings.Contains(r.Diff, "new-secret") {
		t.Fatalf("unmasked diff = %q", r.Diff)
	}
	for _, bad := range []string{"../x", "/etc/passwd", ".git/HEAD"} {
		if _, err := c.AgentSessionFileDiff(ctx, "shop", bad, DiffOptions{}); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	d, err := c.AgentSessionDiff(ctx, "shop")
	if err != nil || d.Files.Modified != 1 || d.Database != nil {
		t.Fatalf("diff = %+v, %v", d, err)
	}
}
