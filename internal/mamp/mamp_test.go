// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package mamp

import (
	"context"
	"crypto/sha256"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/benbowdev/benbow/internal/errcode"
)

// testdata/settings6.xml is a synthetic MAMP PRO archive (made-up hosts
// under /Users/test/Sites) with MAMP's shapes: an NSDictionary root, hosts
// as keyed objects, NSString objects, MysqlItem databases and a cycle.
var want = []Host{
	{ServerName: "localhost", DocumentRoot: "/Users/test/Sites/htdocs", PHPVersion: "7.4.33", Aliases: []string{}, Databases: []string{}},
	{ServerName: "shop.local", DocumentRoot: "/Users/test/Sites/Shop Site/public", PHPVersion: "8.1.13", Aliases: []string{}, Databases: []string{"shop"}},
	{ServerName: "blog.client.local", DocumentRoot: "/Users/test/Sites/blog", PHPVersion: "7.4.33", Aliases: []string{"www.blog.client.local"}, Databases: []string{"blog", "blog_old"}},
	{ServerName: "intranet", DocumentRoot: "/Users/test/Sites/intranet", PHPVersion: "8.2.0", Aliases: []string{}, Databases: []string{}},
	{ServerName: "gone.local", DocumentRoot: "/Users/test/Sites/missing", PHPVersion: "8.2.0", Aliases: []string{}, Databases: []string{}},
	{ServerName: "Bad_Name.local", DocumentRoot: "/Users/test/Sites/shop", PHPVersion: "8.1.13", Aliases: []string{}, Databases: []string{}},
}

func TestReadCopy(t *testing.T) {
	dir := t.TempDir()
	// Binary, like the real file.
	src := filepath.Join(dir, "MAMP PRO", "settings6.plist")
	if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("/usr/bin/plutil", "-convert", "binary1", "-o", src, filepath.Join("testdata", "settings6.xml")).CombinedOutput(); err != nil {
		t.Fatalf("plutil: %v %s", err, out)
	}
	if err := os.Chmod(src, 0o444); err != nil { // read-only, as far as Benbow is concerned
		t.Fatal(err)
	}
	before, _ := os.ReadFile(src)
	infoBefore, _ := os.Stat(src)

	tmp := filepath.Join(dir, "tmp")
	got, err := ReadCopy(context.Background(), src, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hosts:\n%+v\nwant\n%+v", got, want)
	}

	after, _ := os.ReadFile(src)
	infoAfter, _ := os.Stat(src)
	if sha256.Sum256(before) != sha256.Sum256(after) || !infoAfter.ModTime().Equal(infoBefore.ModTime()) || infoAfter.Mode() != infoBefore.Mode() {
		t.Fatal("the original changed")
	}
	if entries, _ := os.ReadDir(tmp); len(entries) != 0 {
		t.Fatalf("copy left behind: %v", entries)
	}
}

func TestReadCopyErrors(t *testing.T) {
	dir := t.TempDir()
	if _, err := ReadCopy(context.Background(), filepath.Join(dir, "none.plist"), dir); errcode.Code(err) != errcode.NotFound {
		t.Fatalf("missing: %v", err)
	}
	junk := filepath.Join(dir, "junk.plist")
	if err := os.WriteFile(junk, []byte("not a plist"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadCopy(context.Background(), junk, dir); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("junk: %v", err)
	}
}

func TestParseRejects(t *testing.T) {
	for name, body := range map[string]string{
		"not keyed": `<plist><dict><key>a</key><string>b</string></dict></plist>`,
		"no root":   `<plist><dict><key>$archiver</key><string>NSKeyedArchiver</string><key>$objects</key><array/><key>$top</key><dict/></dict></plist>`,
		"bad xml":   `<plist><dict><key>a</key>`,
		"bad int":   `<plist><integer>x</integer></plist>`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(body)); errcode.Code(err) != errcode.InvalidInput {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	body, err := os.ReadFile(filepath.Join("testdata", "settings6.xml"))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(body)
	f.Add([]byte(`<plist><dict><key>$archiver</key><string>NSKeyedArchiver</string><key>$objects</key><array><dict><key>CF$UID</key><integer>99</integer></dict></array><key>$top</key><dict><key>root</key><dict><key>CF$UID</key><integer>0</integer></dict></dict></dict></plist>`))
	f.Fuzz(func(_ *testing.T, b []byte) {
		_, _ = Parse(b) // must not panic or hang
	})
}
