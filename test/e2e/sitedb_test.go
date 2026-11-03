// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
)

type siteDB struct {
	Database  string `json:"database"`
	User      string `json:"user"`
	Password  string `json:"password"`
	Port      int    `json:"port"`
	Dedicated bool   `json:"dedicated"`
	Dropped   bool   `json:"dropped"`
}

// The page connects with the site's credentials (db.json) through the
// socket and over TCP, and tries to reach another database (?other=name).
const siteDBPage = `<?php
mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
$c = json_decode(file_get_contents(__DIR__ . '/db.json'), true);
$a = new mysqli('localhost', $c['user'], $c['password'], $c['database']);
$a->query('CREATE TABLE IF NOT EXISTS t (v INT)');
$a->query('INSERT INTO t VALUES (1)');
$b = new mysqli('127.0.0.1', $c['user'], $c['password'], $c['database'], $c['port']);
echo $b->query('SELECT COUNT(*) FROM t')->fetch_row()[0];
if (isset($_GET['other'])) {
	try { $a->select_db($_GET['other']); echo '|reached'; } catch (mysqli_sql_exception $e) { echo '|denied'; }
}
`

func (h *harness) siteDB(args ...string) siteDB {
	h.t.Helper()
	var d siteDB
	if err := json.Unmarshal(h.mustCovo(append([]string{"db"}, args...)...), &d); err != nil {
		h.t.Fatal(err)
	}
	return d
}

func (h *harness) keychainAccount(account string) bool {
	return h.keychainAccountIn(".mysql", account)
}

// keychainAccountIn reports whether Covo's keychain service with suffix
// (".mysql", ".postgres") has an item for account.
func (h *harness) keychainAccountIn(suffix, account string) bool {
	return exec.Command("/usr/bin/security", "find-generic-password", "-a", account, "-s", h.prefix()+suffix).Run() == nil
}

func TestSiteDatabase(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	shop, blog := newSite(t), newSite(t)
	for _, dir := range []string{shop, blog} {
		if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(siteDBPage), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	h.mustCovo("site", "add", "my-shop", "--php", "8.4", "--path", shop)
	h.mustCovo("site", "add", "blog", "--php", "8.4", "--path", blog)

	// MySQL off, then stopped: clear errors.
	if env := h.covo("db", "create", "my-shop"); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("create with MySQL off = %+v", env)
	}
	h.mustCovo("mysql", "enable")
	if env := h.covo("db", "create", "my-shop"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("create with Covo stopped = %+v", env)
	}
	h.mustCovo("start")

	d := h.siteDB("create", "my-shop")
	if d.Database != "my_shop" || d.User != "my_shop" || !d.Dedicated || len(d.Password) != 24 {
		t.Fatalf("create = %+v", d)
	}
	if !h.keychainAccount("my_shop") {
		t.Fatal("site password not in the keychain")
	}
	if env := h.covo("db", "create", "my-shop"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("second create = %+v", env)
	}
	if env := h.covo("db", "create", "blog", "--name", "my_shop"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("create over an existing database = %+v", env)
	}
	if env := h.covo("db", "create", "blog", "--name", "mysql"); env.OK || env.Error.Code != "invalid_input" {
		t.Fatalf("create a system database = %+v", env)
	}
	writeCreds(t, shop, d)

	st := h.status()
	var adm struct {
		AdminerURL string `json:"adminer_url"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &adm); err != nil || adm.AdminerURL != "https://adminer.localhost:"+strconv.Itoa(st.HTTPSPort)+"/" {
		t.Fatalf("adminer_url = %q, %v", adm.AdminerURL, err)
	}
	if body := h.get(adm.AdminerURL, st.CA.CertPath); !strings.Contains(body, "Adminer") || !strings.Contains(body, "Login") {
		t.Fatalf("Adminer page: %.300q", body)
	}
	if env := h.covo("site", "add", "adminer", "--path", shop); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("site named adminer = %+v", env)
	}
	url := func(site string) string { return "https://" + site + ".localhost:" + strconv.Itoa(st.HTTPSPort) }
	if got := h.get(url("my-shop"), st.CA.CertPath); got != "1" {
		t.Fatalf("my-shop page = %q", got)
	}

	// blog: its own database; the shop user cannot reach it.
	b := h.siteDB("create", "blog")
	writeCreds(t, blog, b)
	if got := h.get(url("blog"), st.CA.CertPath); got != "1" {
		t.Fatalf("blog page = %q", got)
	}
	if got := h.get(url("my-shop")+"/?other=blog", st.CA.CertPath); got != "2|denied" {
		t.Fatalf("my-shop reaching blog = %q", got)
	}

	var dbs []string
	if err := json.Unmarshal(h.mustCovo("db", "list"), &dbs); err != nil || !slices.Equal(dbs, []string{"blog", "my_shop"}) {
		t.Fatalf("db list = %v, %v", dbs, err)
	}

	// Unlink keeps the data; link connects as root.
	if u := h.siteDB("unlink", "blog"); u.Dropped {
		t.Fatalf("unlink dropped: %+v", u)
	}
	l := h.siteDB("link", "blog", "blog")
	if l.User != "root" || l.Dedicated {
		t.Fatalf("link = %+v", l)
	}
	if got := h.get(url("blog"), st.CA.CertPath); got != "2" {
		t.Fatalf("blog data after unlink+link = %q", got)
	}
	if env := h.covo("db", "link", "blog", "nope"); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("link while linked = %+v", env)
	}

	// Drop needs confirmation, then deletes the database, user and password.
	if env := h.covo("db", "unlink", "my-shop", "--drop"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("drop without --yes = %+v", env)
	}
	if u := h.siteDB("unlink", "my-shop", "--drop", "--yes"); !u.Dropped {
		t.Fatalf("drop = %+v", u)
	}
	if h.keychainAccount("my_shop") {
		t.Fatal("site password left after drop")
	}
	if err := json.Unmarshal(h.mustCovo("db", "list"), &dbs); err != nil || !slices.Equal(dbs, []string{"blog"}) {
		t.Fatalf("db list after drop = %v, %v", dbs, err)
	}
	// The user is gone too: creating it again works.
	h.siteDB("create", "my-shop")

	// Removing a site with its database.
	if env := h.covo("site", "remove", "my-shop", "--drop-database"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("remove --drop-database without --yes = %+v", env)
	}
	h.mustCovo("site", "remove", "my-shop", "--drop-database", "--yes")
	if err := json.Unmarshal(h.mustCovo("db", "list"), &dbs); err != nil || !slices.Equal(dbs, []string{"blog"}) {
		t.Fatalf("db list after remove --drop-database = %v, %v", dbs, err)
	}
	if h.keychainAccount("my_shop") {
		t.Fatal("site password left after remove --drop-database")
	}
	if _, err := os.Stat(shop); err != nil {
		t.Fatalf("site folder touched: %v", err)
	}

	h.mustCovo("uninstall", "--yes")
	if h.keychainAccount("my_shop") || h.keychainItem() {
		t.Fatal("keychain items left after uninstall")
	}
}

func writeCreds(t *testing.T, dir string, d siteDB) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"user": d.User, "password": d.Password, "database": d.Database, "port": d.Port})
	if err := os.WriteFile(filepath.Join(dir, "db.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}
