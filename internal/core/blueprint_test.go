// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"archive/zip"
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/store"
)

func TestParseBlueprint(t *testing.T) {
	tests := []struct {
		name, json, err string
	}{
		{"minimal", `{"name":"agency"}`, ""},
		{"full", `{"name":"agency","wordpress":{"version":"6.8.1","locale":"it_IT"},
			"themes":[{"slug":"astra","activate":true}],
			"plugins":[{"slug":"contact-form-7","version":"6.0"},{"url":"https://example.com/p.zip"},{"github":"acme/tools","ref":"v1.2"},{"git":"git@github.com:acme/x.git"}],
			"options":{"permalinks":"/%postname%/","timezone":"Europe/Rome","remove_sample_content":true,"set":{"blogdescription":"Hi"}},
			"constants":{"WP_DEBUG":true,"WP_MEMORY_LIMIT":"256M","EMPTY_TRASH_DAYS":7},
			"mu_plugins":[{"name":"a.php","content":"<?php // a"}]}`, ""},
		{"bad name", `{"name":"Agency"}`, "name"},
		{"unknown field", `{"name":"a","plugin":[]}`, "unknown field"},
		{"no source", `{"name":"a","plugins":[{"activate":true}]}`, "needs a slug"},
		{"two sources", `{"name":"a","plugins":[{"url":"https://x.dev/a.zip","github":"a/b"}]}`, "only one"},
		{"http url", `{"name":"a","plugins":[{"url":"http://example.com/a.zip"}]}`, "https"},
		{"http loopback", `{"name":"a","plugins":[{"url":"http://127.0.0.1:8080/a.zip"}]}`, ""},
		{"url with user", `{"name":"a","plugins":[{"url":"https://u:p@example.com/a.zip"}]}`, "not valid"},
		{"version on url", `{"name":"a","plugins":[{"url":"https://x.dev/a.zip","version":"1"}]}`, "version"},
		{"dash ref", `{"name":"a","plugins":[{"github":"a/b","ref":"-upload-pack=x"}]}`, "ref"},
		{"dotdot ref", `{"name":"a","plugins":[{"github":"a/b","ref":"a/../b"}]}`, "ref"},
		{"git option", `{"name":"a","plugins":[{"git":"--upload-pack=x"}]}`, "git"},
		{"slug path", `{"name":"a","plugins":[{"slug":"../x"}]}`, "slug"},
		{"two active themes", `{"name":"a","themes":[{"slug":"a","activate":true},{"slug":"b","activate":true}]}`, "one theme"},
		{"db constant", `{"name":"a","constants":{"DB_PASSWORD":"x"}}`, "DB_PASSWORD"},
		{"salt constant", `{"name":"a","constants":{"AUTH_KEY":"x"}}`, "AUTH_KEY"},
		{"object constant", `{"name":"a","constants":{"X":{"a":1}}}`, "value"},
		{"newline constant", `{"name":"a","constants":{"X":"a\nb"}}`, "one line"},
		{"siteurl option", `{"name":"a","options":{"set":{"siteurl":"x"}}}`, "siteurl"},
		{"permalinks", `{"name":"a","options":{"permalinks":"postname"}}`, "permalinks"},
		{"timezone", `{"name":"a","options":{"timezone":"Rome; rm"}}`, "timezone"},
		{"locale", `{"name":"a","wordpress":{"locale":"italian"}}`, "locale"},
		{"mu name", `{"name":"a","mu_plugins":[{"name":"../a.php","content":"<?php"}]}`, "must-use"},
		{"mu content", `{"name":"a","mu_plugins":[{"name":"a.php","content":"hello"}]}`, "<?php"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseBlueprint([]byte(tt.json))
			if tt.err == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.err) {
				t.Fatalf("error = %v, want %q", err, tt.err)
			}
			if errcode.Code(err) != errcode.InvalidInput {
				t.Fatalf("code = %s", errcode.Code(err))
			}
		})
	}
}

type zipEntry struct {
	name, body string
	mode       fs.FileMode
}

func makeZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		h := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		if e.mode != 0 {
			h.SetMode(e.mode)
		}
		w, err := zw.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestUnzipPackage(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
		root    string // relative to the extraction folder; "." = itself
		err     string
	}{
		{"single folder", []zipEntry{{name: "hello/hello.php", body: "<?php"}, {name: "__MACOSX/._x"}}, "hello", ""},
		{"flat", []zipEntry{{name: "a.php", body: "<?php"}, {name: "b.php"}}, ".", ""},
		{"dotdot", []zipEntry{{name: "../evil.php"}}, "", "outside"},
		{"absolute", []zipEntry{{name: "/etc/evil"}}, "", "outside"},
		{"backslash", []zipEntry{{name: `a\..\..\evil`}}, "", "outside"},
		{"link", []zipEntry{{name: "x/link", body: "/etc/passwd", mode: fs.ModeSymlink | 0o777}}, "", "plain file"},
		{"empty", []zipEntry{{name: "__MACOSX/._x"}}, "", "empty"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			file := filepath.Join(dir, "p.zip")
			if err := os.WriteFile(file, makeZip(t, tt.entries...), 0o600); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "x")
			root, err := unzipPackage(file, out)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if want := filepath.Join(out, tt.root); root != want {
				t.Fatalf("root = %s, want %s", root, want)
			}
			if _, err := os.Stat(filepath.Join(dir, "evil.php")); err == nil {
				t.Fatal("wrote outside")
			}
		})
	}
}

func TestInstallPackageRecordsSHA256(t *testing.T) {
	ctx := context.Background()
	body := makeZip(t, zipEntry{name: "hello-dolly/hello.php", body: "<?php // v1"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/plugins/info/1.2/"):
			if r.URL.Query().Get("request[slug]") != "hello-dolly" {
				_, _ = w.Write([]byte(`{"error":"Plugin not found."}`))
				return
			}
			_, _ = w.Write([]byte(`{"slug":"hello-dolly","version":"1.7.2"}`))
		case r.URL.Path == "/plugin/hello-dolly.1.7.2.zip", r.URL.Path == "/own.zip":
			_, _ = w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	oldAPI, oldDL := wporgAPI, wporgDownloads
	wporgAPI, wporgDownloads = srv.URL, srv.URL
	defer func() { wporgAPI, wporgDownloads = oldAPI, oldDL }()

	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "covo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	c := &Core{Store: st, Layout: paths.Layout{Tmp: filepath.Join(dir, "tmp")}}
	plugins := filepath.Join(dir, "site", "wp-content", "plugins")

	install := func(p BlueprintPackage, accept bool) (string, *BlueprintResult, error) {
		res := &BlueprintResult{}
		slug, _, err := c.installPackage(ctx, "plugin", p, plugins, accept, res)
		return slug, res, err
	}
	slug, _, err := install(BlueprintPackage{Slug: "hello-dolly"}, false)
	if err != nil || slug != "hello-dolly" {
		t.Fatalf("install = %q, %v", slug, err)
	}
	if got, _ := os.ReadFile(filepath.Join(plugins, "hello-dolly", "hello.php")); string(got) != "<?php // v1" {
		t.Fatalf("installed file = %q", got)
	}
	srcs, _ := st.BlueprintSources(ctx)
	if len(srcs) != 1 || srcs[0].URL != srv.URL+"/plugin/hello-dolly.1.7.2.zip" || len(srcs[0].SHA256) != 64 {
		t.Fatalf("sources = %+v", srcs)
	}
	if _, res, _ := install(BlueprintPackage{Slug: "hello-dolly"}, false); len(res.Warnings) != 1 {
		t.Fatalf("second install should be skipped with a warning: %+v", res)
	}
	if _, _, err := install(BlueprintPackage{Slug: "missing"}, false); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("missing plugin error = %v", err)
	}

	// A zip URL: the folder comes from the zip. Then its content changes.
	own := BlueprintPackage{URL: srv.URL + "/own.zip"}
	_ = os.RemoveAll(filepath.Join(plugins, "hello-dolly"))
	if slug, _, err := install(own, false); err != nil || slug != "hello-dolly" {
		t.Fatalf("zip install = %q, %v", slug, err)
	}
	_ = os.RemoveAll(filepath.Join(plugins, "hello-dolly"))
	body = makeZip(t, zipEntry{name: "hello-dolly/hello.php", body: "<?php // tampered"})
	if _, _, err := install(own, false); errcode.Code(err) != errcode.Checksum || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("changed zip error = %v", err)
	}
	if exists(filepath.Join(plugins, "hello-dolly")) {
		t.Fatal("a changed package was installed")
	}
	if _, res, err := install(own, true); err != nil || len(res.Warnings) != 1 {
		t.Fatalf("accepted change = %+v, %v", res, err)
	}
}

func TestTreeSHA256(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	for _, d := range []string{a, b} {
		_ = os.MkdirAll(filepath.Join(d, "x"), 0o755)
		_ = os.WriteFile(filepath.Join(d, "x", "f.php"), []byte("<?php"), 0o644)
	}
	ha, _ := treeSHA256(a)
	hb, _ := treeSHA256(b)
	if ha != hb || len(ha) != 64 {
		t.Fatalf("same trees hash differently: %s %s", ha, hb)
	}
	_ = os.WriteFile(filepath.Join(b, "x", "f.php"), []byte("<?php //"), 0o644)
	if hb, _ = treeSHA256(b); ha == hb {
		t.Fatal("changed tree has the same hash")
	}
	_ = os.Symlink("/etc/passwd", filepath.Join(a, "link"))
	if _, err := treeSHA256(a); err == nil {
		t.Fatal("link accepted")
	}
}
