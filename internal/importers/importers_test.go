// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package importers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/benbowdev/benbow/internal/mamp"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadLocal(t *testing.T) {
	home := t.TempDir()
	write(t, filepath.Join(home, "Local Sites", "shop", "app", "public", "index.php"), "<?php")
	write(t, filepath.Join(home, "Library", "Application Support", "Local", "sites.json"), `{
	  "abc123": {"name": "Shop", "domain": "shop.local", "path": "~/Local Sites/shop",
	    "services": {"php": {"version": "8.2.27"}, "mysql": {"version": "8.0.35", "ports": {"MYSQL": [10005]}}},
	    "mysql": {"database": "local", "user": "root", "password": "root"}},
	  "broken": {"name": "x"},
	  "def456": {"name": "Old", "domain": "old.local", "path": "/nowhere/old", "services": {"mysql": {"ports": {}}}}
	}`)
	hosts, err := ReadLocal(home)
	if err != nil {
		t.Fatal(err)
	}
	if len(hosts) != 2 {
		t.Fatalf("hosts = %+v", hosts)
	}
	shop := hosts[1]
	if shop.ServerName != "shop.local" || shop.DocumentRoot != filepath.Join(home, "Local Sites", "shop", "app", "public") ||
		shop.PHPVersion != "8.2.27" || shop.DB.Port != 10005 || shop.DB.Password != "root" ||
		!strings.HasSuffix(shop.DB.Socket, "/run/abc123/mysql/mysqld.sock") || shop.Databases[0] != "local" {
		t.Fatalf("shop = %+v %+v", shop, shop.DB)
	}
	if old := hosts[0]; old.DocumentRoot != "/nowhere/old" || old.DB.Port != 0 || old.DB.User != "root" {
		t.Fatalf("old = %+v %+v", old, old.DB)
	}
	if _, err := ReadLocal(t.TempDir()); err == nil || !strings.Contains(err.Error(), "Local not found") {
		t.Fatalf("missing Local err = %v", err)
	}
}

func TestReadValetAndHerd(t *testing.T) {
	home := t.TempDir()
	parked := filepath.Join(home, "Sites")
	write(t, filepath.Join(parked, "blog", "index.php"), "<?php")
	write(t, filepath.Join(parked, "api", "artisan"), "")
	write(t, filepath.Join(parked, ".hidden", "index.php"), "")
	linked := filepath.Join(home, "Work", "Client Site")
	write(t, filepath.Join(linked, "index.php"), "<?php")
	for _, tt := range []struct {
		read func(string) ([]string, error)
		dir  string
	}{
		{func(h string) ([]string, error) { return names(ReadValet(h)) }, filepath.Join(home, ".config", "valet")},
		{func(h string) ([]string, error) { return names(ReadHerd(h)) }, filepath.Join(home, "Library", "Application Support", "Herd", "config", "valet")},
	} {
		write(t, filepath.Join(tt.dir, "config.json"), `{"tld": "test", "paths": ["`+parked+`"]}`)
		write(t, filepath.Join(tt.dir, "Nginx", "client.test"), "# ISOLATED_PHP_VERSION=php@8.1\nserver {}")
		_ = os.MkdirAll(filepath.Join(tt.dir, "Sites"), 0o755)
		_ = os.Symlink(linked, filepath.Join(tt.dir, "Sites", "client"))
		got, err := tt.read(home)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Join(got, ",") != "api.test,blog.test,client.test=8.1" {
			t.Fatalf("%s: %v", tt.dir, got)
		}
	}
}

func names(hs []mamp.Host, err error) ([]string, error) {
	var out []string
	for _, h := range hs {
		n := h.ServerName
		if h.PHPVersion != "" {
			n += "=" + h.PHPVersion
		}
		out = append(out, n)
	}
	return out, err
}

func TestReadXAMPP(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "Applications", "XAMPP", "xamppfiles")
	write(t, filepath.Join(dir, "bin", "php-config"), "#! /bin/sh\nprefix=\"/x\"\nversion=\"8.2.4\"\n")
	write(t, filepath.Join(dir, "etc", "extra", "httpd-vhosts.conf"), `# example
<VirtualHost *:80>
    ServerName shop.test
    ServerAlias www.shop.test
    DocumentRoot "/Users/x/Sites/my shop"
</VirtualHost>
#<VirtualHost *:80>
#    ServerName commented.test
#</VirtualHost>
<VirtualHost *:80>
    DocumentRoot "/nameless"
</VirtualHost>
`)
	write(t, filepath.Join(dir, "etc", "httpd.conf"), "Listen 80\n#Include etc/extra/httpd-vhosts.conf\n")
	hosts, err := ReadXAMPP(root)
	if err != nil || len(hosts) != 0 {
		t.Fatalf("vhosts not included: %+v, %v", hosts, err)
	}
	write(t, filepath.Join(dir, "etc", "httpd.conf"), "Listen 80\nInclude etc/extra/httpd-vhosts.conf\n")
	hosts, err = ReadXAMPP(root)
	if err != nil || len(hosts) != 1 {
		t.Fatalf("hosts = %+v, %v", hosts, err)
	}
	h := hosts[0]
	if h.ServerName != "shop.test" || h.DocumentRoot != "/Users/x/Sites/my shop" || h.PHPVersion != "8.2" ||
		strings.Join(h.Aliases, ",") != "www.shop.test" || !strings.HasSuffix(h.DB.Socket, "var/mysql/mysql.sock") {
		t.Fatalf("host = %+v", h)
	}
}
