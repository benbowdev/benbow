// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type mysqlStatus struct {
	Enabled        bool   `json:"enabled"`
	Version        string `json:"version"`
	Port           int    `json:"port"`
	Socket         string `json:"socket"`
	DataDir        string `json:"data_dir"`
	Initialized    bool   `json:"initialized"`
	PasswordStored bool   `json:"password_stored"`
	Running        bool   `json:"running"`
}

func (h *harness) mysqlStatus() mysqlStatus {
	h.t.Helper()
	var st mysqlStatus
	if err := json.Unmarshal(h.mustBenbow("mysql"), &st); err != nil {
		h.t.Fatal(err)
	}
	return st
}

// keychainItem reports whether the login keychain holds the MySQL password
// of this test installation (attributes only).
func (h *harness) keychainItem() bool {
	prefix := ""
	for _, kv := range h.env {
		if v, ok := strings.CutPrefix(kv, "BENBOW_LABEL_PREFIX="); ok {
			prefix = v
		}
	}
	return exec.Command("/usr/bin/security", "find-generic-password", "-a", "root", "-s", prefix+".mysql").Run() == nil
}

// The PHP page connects through the socket ("localhost") and through
// 127.0.0.1 without a port: php.ini must send both to Benbow's MySQL.
const mysqlPage = `<?php
mysqli_report(MYSQLI_REPORT_ERROR | MYSQLI_REPORT_STRICT);
$pw = file_get_contents(__DIR__ . '/pw.txt');
$a = new mysqli('localhost', 'root', $pw);
$a->query('CREATE DATABASE IF NOT EXISTS benbow_e2e');
$a->query('CREATE TABLE IF NOT EXISTS benbow_e2e.t (v VARCHAR(20))');
if (isset($_GET['write'])) { $a->query("INSERT INTO benbow_e2e.t VALUES ('kept')"); }
$b = new mysqli('127.0.0.1', 'root', $pw);
$pdo = new PDO('mysql:host=localhost', 'root', $pw);
echo $a->query('SELECT VERSION()')->fetch_row()[0], '|',
	$b->query('SELECT @@port')->fetch_row()[0], '|',
	$pdo->query("SELECT COUNT(*) FROM benbow_e2e.t WHERE v='kept'")->fetchColumn(), '|',
	$a->query("SELECT 'a1' REGEXP '^[a-z][0-9]$'")->fetch_row()[0];
`

func TestMySQL(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)

	// Enabling while stopped installs and initializes but starts nothing.
	var res struct {
		mysqlStatus
		Applied bool `json:"applied"`
	}
	if err := json.Unmarshal(h.mustBenbow("mysql", "enable"), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || !res.Initialized || !res.PasswordStored || res.Applied || res.Running || res.Version != "8.4.13" {
		t.Fatalf("enable = %+v", res)
	}
	if res.Port < 3307 {
		t.Fatalf("port = %d", res.Port)
	}
	if !h.keychainItem() {
		t.Fatal("password not in the keychain")
	}

	var cred struct {
		Port     int    `json:"port"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(h.mustBenbow("mysql", "password"), &cred); err != nil {
		t.Fatal(err)
	}
	if len(cred.Password) != 24 || cred.Port != res.Port {
		t.Fatalf("credentials = %+v", cred)
	}

	url, dir := startedSite(t, h, mysqlPage)
	siteDir := siteDirOf(t, h)
	if err := os.WriteFile(filepath.Join(siteDir, "pw.txt"), []byte(cred.Password), 0o600); err != nil {
		t.Fatal(err)
	}
	if st := h.mysqlStatus(); !st.Running {
		t.Fatalf("mysql not running after start: %+v", st)
	}
	body := h.get(url+"/?write=1", dir.RootCert())
	want := "8.4.13|" + strconv.Itoa(res.Port) + "|1|1"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}

	// Only 127.0.0.1:<port>: no other interface, no X Protocol port.
	var status struct {
		Services []struct {
			Name string `json:"name"`
			PID  int    `json:"pid"`
		} `json:"services"`
	}
	if err := json.Unmarshal(h.mustBenbow("status"), &status); err != nil {
		t.Fatal(err)
	}
	pid := 0
	for _, svc := range status.Services {
		if svc.Name == "mysql" {
			pid = svc.PID
		}
	}
	if pid == 0 {
		t.Fatalf("no mysql service in status: %+v", status.Services)
	}
	out, err := exec.Command("/usr/sbin/lsof", "-nP", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-F", "n").Output()
	if err != nil {
		t.Fatalf("lsof: %v", err)
	}
	var listen []string
	for line := range strings.SplitSeq(string(out), "\n") {
		if n, ok := strings.CutPrefix(line, "n"); ok {
			listen = append(listen, n)
		}
	}
	if len(listen) != 1 || listen[0] != "127.0.0.1:"+strconv.Itoa(res.Port) {
		t.Fatalf("mysqld listens on %v, want only 127.0.0.1:%d", listen, res.Port)
	}

	// The password is nowhere in Benbow's home outside the data directory.
	home := homeOf(t, h)
	_ = filepath.WalkDir(home, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			if d != nil && d.IsDir() && p == res.DataDir {
				return filepath.SkipDir
			}
			return nil
		}
		if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), cred.Password) {
			t.Errorf("password found in %s", p)
		}
		return nil
	})

	// Data survives stop/start and disable/enable.
	h.mustBenbow("stop")
	h.mustBenbow("start")
	if got := h.get(url, dir.RootCert()); got != want {
		t.Fatalf("after restart body = %q", got)
	}
	h.mustBenbow("mysql", "disable")
	if st := h.mysqlStatus(); st.Enabled || st.Running || !st.Initialized {
		t.Fatalf("after disable: %+v", st)
	}
	h.mustBenbow("mysql", "enable")
	if got := h.get(url, dir.RootCert()); got != want {
		t.Fatalf("after re-enable body = %q", got)
	}

	// Uninstall removes the data and the keychain item.
	var plan struct {
		Keychain []string `json:"keychain"`
	}
	if err := json.Unmarshal(h.mustBenbow("uninstall", "--yes"), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Keychain) != 1 {
		t.Fatalf("uninstall keychain = %v", plan.Keychain)
	}
	if h.keychainItem() {
		t.Fatal("keychain item left after uninstall")
	}
	if _, err := os.Stat(res.DataDir); !os.IsNotExist(err) {
		t.Fatalf("data dir left after uninstall: %v", err)
	}
}

func TestMySQLPortInUse(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	busy := l.Addr().(*net.TCPAddr).Port
	env := h.benbow("mysql", "enable", "--port", strconv.Itoa(busy))
	if env.OK || env.Error.Code != "port_in_use" {
		t.Fatalf("enable on busy port: %+v", env)
	}
	if h.keychainItem() {
		t.Fatal("refused enable left a keychain item")
	}
}

func siteDirOf(t *testing.T, h *harness) string {
	t.Helper()
	var sites []struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "list"), &sites); err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("sites = %+v", sites)
	}
	return sites[0].Path
}

func homeOf(t *testing.T, h *harness) string {
	t.Helper()
	var st struct {
		Home string `json:"home"`
	}
	if err := json.Unmarshal(h.mustBenbow("status"), &st); err != nil {
		t.Fatal(err)
	}
	return st.Home
}
