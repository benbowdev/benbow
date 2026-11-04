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
	"strconv"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/wpconfig"
)

// fakeSSH stands in for /usr/bin/ssh: it runs the remote command (or the
// sftp subsystem) on this Mac, so "the server" is a local folder.
const fakeSSH = `#!/bin/sh
export PATH="$COVO_TEST_REMOTE_PATH:$PATH"
sub=0
while [ $# -gt 0 ]; do
  case "$1" in
    -o|-p|-l) shift 2 ;;
    -s) sub=1; shift ;;
    --) shift; break ;;
    -*) shift ;;
    *) break ;;
  esac
done
shift
if [ "$sub" = 1 ]; then exec /usr/libexec/sftp-server; fi
exec /bin/sh -c "$*"
`

// TestSync pulls a WordPress site from "production" and pushes to it as
// staging, over SSH (rsync, mysqldump) and over SFTP with the PHP bridge.
// The remote is another site of the same Covo, in its own folder and
// database.
func TestSync(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")
	base := t.TempDir()
	local, prod := filepath.Join(base, "shop"), filepath.Join(base, "prod")
	h.mustCovo("wordpress", "shop", "--path", local)

	// Production: a copy of the site with its own database and address.
	if out, err := exec.Command("/bin/cp", "-R", local, prod).CombinedOutput(); err != nil {
		t.Fatalf("cp: %v %s", err, out)
	}
	h.mustCovo("site", "add", "prod", "--path", prod, "--php", "8.4", "--database", "new")
	var db struct {
		Database, User, Password string
		Port                     int
	}
	if err := json.Unmarshal(h.mustCovo("db", "show", "prod"), &db); err != nil {
		t.Fatal(err)
	}
	cfgPath := filepath.Join(prod, "wp-config.php")
	b, _ := os.ReadFile(cfgPath)
	cfg, err := wpconfig.Set(string(b), map[string]string{"DB_NAME": db.Database, "DB_USER": db.User, "DB_PASSWORD": db.Password,
		"DB_HOST": "127.0.0.1:" + strconv.Itoa(db.Port)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	var sites []struct {
		Name, URL string
	}
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil {
		t.Fatal(err)
	}
	urls := map[string]string{}
	for _, s := range sites {
		urls[s.Name] = s.URL
	}
	dump := filepath.Join(base, "shop.sql")
	wp := func(site string, args ...string) string {
		t.Helper()
		out, code := h.raw(append([]string{"wp", site}, args...)...)
		if code != 0 {
			t.Fatalf("wp %s %v: %s", site, args, out)
		}
		return strings.TrimSpace(out)
	}
	wp("shop", "db", "export", dump)
	wp("prod", "db", "import", dump)
	wp("prod", "search-replace", urls["shop"], urls["prod"], "--quiet")
	wp("prod", "option", "update", "blogname", "Production Blog")
	uploads := filepath.Join(prod, "wp-content", "uploads")
	if err := os.MkdirAll(uploads, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uploads, "prod.txt"), []byte("from production"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The fake ssh, with the MySQL client tools on "the server".
	fake := filepath.Join(base, "ssh")
	if err := os.WriteFile(fake, []byte(fakeSSH), 0o755); err != nil {
		t.Fatal(err)
	}
	st := h.status()
	mysqlBins, _ := filepath.Glob(filepath.Join(st.Home, "bin", "mysql", "*", "bin"))
	if len(mysqlBins) == 0 {
		t.Fatal("no MySQL tools")
	}
	h.env = append(h.env, "COVO_SSH="+fake, "COVO_TEST_REMOTE_PATH="+mysqlBins[0], "COVO_BRIDGE_CA="+st.CA.CertPath)

	h.mustCovo("remote", "set", "shop", "--kind", "staging", "--name", "stage", "--host", "fakehost", "--user", "deploy",
		"--path", prod, "--url", urls["prod"])

	type result struct {
		Files    []string `json:"files"`
		Database string   `json:"database"`
		URLFrom  string   `json:"url_from"`
		Snapshot string   `json:"snapshot"`
	}
	var dry result
	if err := json.Unmarshal(h.mustCovo("pull", "shop", "stage", "--dry-run"), &dry); err != nil || len(dry.Files) != 1 || dry.Snapshot != "" {
		t.Fatalf("dry pull = %+v, %v", dry, err)
	}
	if _, err := os.Stat(filepath.Join(local, "wp-content", "uploads", "prod.txt")); !os.IsNotExist(err) {
		t.Fatal("dry run copied a file")
	}

	// Pull over SSH.
	var pulled result
	data := h.mustCovo("pull", "shop", "stage")
	if err := json.Unmarshal(data, &pulled); err != nil || pulled.Snapshot == "" || pulled.URLFrom != urls["prod"] {
		t.Fatalf("pull = %s, %v", data, err)
	}
	if got := wp("shop", "option", "get", "blogname"); got != "Production Blog" {
		t.Fatalf("blogname after pull = %q", got)
	}
	if got := wp("shop", "option", "get", "home"); got != urls["shop"] {
		t.Fatalf("home after pull = %q, want %s", got, urls["shop"])
	}
	if b, err := os.ReadFile(filepath.Join(local, "wp-content", "uploads", "prod.txt")); err != nil || string(b) != "from production" {
		t.Fatalf("uploads after pull: %q, %v", b, err)
	}

	// Push to staging over SSH.
	wp("shop", "option", "update", "blogname", "Local Change")
	if err := os.WriteFile(filepath.Join(local, "wp-content", "uploads", "local.txt"), []byte("from local"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustCovo("push", "shop", "stage", "--yes")
	if got := wp("prod", "option", "get", "blogname"); got != "Local Change" {
		t.Fatalf("staging blogname after push = %q", got)
	}
	if got := wp("prod", "option", "get", "home"); got != urls["prod"] {
		t.Fatalf("staging home after push = %q", got)
	}
	if _, err := os.Stat(filepath.Join(uploads, "local.txt")); err != nil {
		t.Fatal("upload not pushed:", err)
	}
	if b2, _ := os.ReadFile(cfgPath); string(b2) != cfg {
		t.Fatal("push changed the remote wp-config.php")
	}

	// Production is never pushed to.
	h.mustCovo("remote", "set", "shop", "--kind", "production", "--host", "fakehost", "--user", "deploy", "--path", prod, "--url", urls["prod"])
	if env := h.covo("push", "shop", "production", "--yes"); env.OK || env.Error.Code != "unsupported" {
		t.Fatalf("push to production = %+v", env)
	}

	// Pull over SFTP: the database comes through the PHP bridge.
	wp("prod", "option", "update", "blogname", "Through The Bridge")
	h.mustCovo("remote", "set", "shop", "--kind", "staging", "--name", "files", "--transport", "sftp", "--host", "fakehost",
		"--user", "deploy", "--path", prod, "--url", urls["prod"])
	h.mustCovo("pull", "shop", "files")
	if got := wp("shop", "option", "get", "blogname"); got != "Through The Bridge" {
		t.Fatalf("blogname after the bridge pull = %q", got)
	}
	if got := wp("shop", "option", "get", "home"); got != urls["shop"] {
		t.Fatalf("home after the bridge pull = %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(prod, "covo-bridge-*.php")); len(left) != 0 {
		t.Fatalf("bridge left on the server: %v", left)
	}
	// Push through the bridge too.
	wp("shop", "option", "update", "blogname", "Bridge Push")
	h.mustCovo("push", "shop", "files", "--yes", "--no-files")
	if got := wp("prod", "option", "get", "blogname"); got != "Bridge Push" {
		t.Fatalf("staging blogname after the bridge push = %q", got)
	}
	if left, _ := filepath.Glob(filepath.Join(prod, "covo-bridge-*.php")); len(left) != 0 {
		t.Fatalf("bridge left on the server: %v", left)
	}
}
