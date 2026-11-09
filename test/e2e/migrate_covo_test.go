// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// covoTag is the last release under Benbow's former name, Covo.
const covoTag = "v0.1.0-beta.5"

// oldCovo is a real Covo, built from its release tag, in its own home.
type oldCovo struct {
	t      *testing.T
	bin    string
	env    []string
	home   string
	prefix string
	caName string
}

func newOldCovo(t *testing.T, h *harness) *oldCovo {
	t.Helper()
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if out, err := exec.Command("git", "-C", "../..", "worktree", "add", "--detach", src, covoTag).CombinedOutput(); err != nil {
		t.Fatalf("checkout %s: %v\n%s", covoTag, err, out)
	}
	t.Cleanup(func() { _ = exec.Command("git", "-C", "../..", "worktree", "remove", "--force", src).Run() })
	bin := filepath.Join(tmp, "covo")
	build := exec.Command("go", "build", "-tags", "covotest", "-o", bin, "./cmd/covo")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build covo %s: %v\n%s", covoTag, err, out)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	o := &oldCovo{
		t: t, bin: bin, home: filepath.Join(tmp, "Covo Home"),
		// Not under Benbow's prefix nor under the real Covo's.
		prefix: "dev.covomigtest-" + hex.EncodeToString(suffix),
		caName: "Covo Migration Test CA " + hex.EncodeToString(suffix),
	}
	o.env = append(os.Environ(),
		"COVO_HOME="+o.home, "COVO_LABEL_PREFIX="+o.prefix, "COVO_CA_NAME="+o.caName,
		"COVO_HTTPS_PORT="+strconv.Itoa(freePort(t)), "COVO_RESOLVER_DIR="+h.resolverDir, "COVO_NO_UPDATE_CHECK=1")
	t.Cleanup(func() {
		// Whatever happened, no test agent or keychain item stays behind.
		for _, label := range loadedWithPrefix(t, o.prefix) {
			_ = exec.Command("/bin/launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+label).Run()
		}
		for _, svc := range []string{"mysql", "postgres", "remote", "github", "appenv"} {
			for range 100 { // one item per call
				if exec.Command("/usr/bin/security", "delete-generic-password", "-s", o.prefix+"."+svc).Run() != nil {
					break
				}
			}
		}
	})
	return o
}

func (o *oldCovo) run(args ...string) json.RawMessage {
	o.t.Helper()
	cmd := exec.Command(o.bin, append(args, "--json")...)
	cmd.Env = o.env
	out, err := cmd.Output()
	var env envelope
	if jerr := json.Unmarshal(out, &env); jerr != nil || !env.OK {
		o.t.Fatalf("covo %s: %v %+v\n%s", strings.Join(args, " "), err, env.Error, out)
	}
	return env.Data
}

func loadedWithPrefix(t *testing.T, prefix string) []string {
	out, err := exec.Command("/bin/launchctl", "list").Output()
	if err != nil {
		t.Fatal(err)
	}
	var labels []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 && strings.HasPrefix(f[2], prefix+".") {
			labels = append(labels, f[2])
		}
	}
	return labels
}

// TestMigrateFromCovo moves a real Covo installation (a PHP site with its
// own MySQL database, a snapshot, .test) into Benbow and checks that
// everything works there and nothing of Covo is left running or stored.
func TestMigrateFromCovo(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	old := newOldCovo(t, h)

	site := newSite(t)
	if err := os.WriteFile(filepath.Join(site, "index.php"), []byte(siteDBPage), 0o644); err != nil {
		t.Fatal(err)
	}
	old.run("start")
	old.run("site", "add", "shop", "--php", "8.4", "--path", site)
	old.run("mysql", "enable")
	var db struct {
		Database string `json:"database"`
		User     string `json:"user"`
		Password string `json:"password"`
		Port     int    `json:"port"`
	}
	if err := json.Unmarshal(old.run("db", "create", "shop"), &db); err != nil || db.Password == "" {
		t.Fatalf("db create: %+v %v", db, err)
	}
	creds, _ := json.Marshal(map[string]any{"user": db.User, "password": db.Password, "database": db.Database, "port": db.Port})
	if err := os.WriteFile(filepath.Join(site, "db.json"), creds, 0o600); err != nil {
		t.Fatal(err)
	}
	var snap struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(old.run("snapshot", "create", "shop"), &snap); err != nil || snap.ID == "" {
		t.Fatalf("snapshot: %+v %v", snap, err)
	}
	old.run("domains", "enable", "test")
	if len(loadedWithPrefix(t, old.prefix)) == 0 {
		t.Fatal("Covo is not running")
	}

	// A dry run changes nothing.
	migrate := func(args ...string) envelope {
		t.Helper()
		cmd := exec.Command(h.bin, append([]string{"migrate", "covo", "--no-trust", "--json"}, args...)...)
		cmd.Env = append(append([]string{}, h.env...),
			"BENBOW_MIGRATE_FROM="+old.home, "BENBOW_MIGRATE_LABEL_PREFIX="+old.prefix, "BENBOW_MIGRATE_CA_NAME="+old.caName)
		out, _ := cmd.Output()
		var env envelope
		if err := json.Unmarshal(out, &env); err != nil {
			t.Fatalf("migrate: %v\n%s", err, out)
		}
		return env
	}
	if env := migrate("--dry-run"); !env.OK {
		t.Fatalf("dry run: %+v", env.Error)
	}
	if _, err := os.Stat(old.home); err != nil {
		t.Fatalf("the dry run touched Covo's folder: %v", err)
	}

	env := migrate()
	var res struct {
		Sites    []string `json:"sites"`
		Warnings []string `json:"warnings"`
		Finished bool     `json:"finished"`
		Steps    []struct {
			Step, Status, Detail string
		} `json:"steps"`
	}
	if !env.OK || json.Unmarshal(env.Data, &res) != nil {
		t.Fatalf("migrate: %+v\n%s", env.Error, env.Data)
	}
	if !res.Finished || len(res.Warnings) != 0 || strings.Join(res.Sites, ",") != "shop" {
		t.Fatalf("migration = %+v", res)
	}

	// Covo is gone: no folder, no agent, no keychain item.
	if _, err := os.Stat(old.home); !os.IsNotExist(err) {
		t.Errorf("Covo's folder is still there: %v", err)
	}
	if l := loadedWithPrefix(t, old.prefix); len(l) != 0 {
		t.Errorf("Covo agents still loaded: %v", l)
	}
	if exec.Command("/usr/bin/security", "find-generic-password", "-s", old.prefix+".mysql").Run() == nil {
		t.Error("Covo's keychain items are still there")
	}

	// Benbow serves the site, with its database and the same password.
	st := h.status()
	if !st.Running || len(st.Sites) != 1 {
		t.Fatalf("status = %+v", st)
	}
	if got := h.get(st.Sites[0].URL+"/", st.CA.CertPath); got != "2" {
		t.Errorf("site with its database = %q, want the row Covo's request wrote plus this one", got)
	}
	var shown struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(h.mustBenbow("db", "show", "shop"), &shown); err != nil || shown.Password != db.Password {
		t.Errorf("site database password after the move: %v", err)
	}
	if !h.keychainAccountIn(".mysql", "root") {
		t.Error("MySQL's root password is not in Benbow's keychain service")
	}
	// The read-only user is Benbow's own, made on first use.
	h.mustBenbow("db", "query", "shop", "SELECT 1")

	// The snapshot's saved paths point into Benbow's folder: it restores.
	h.mustBenbow("snapshot", "restore", "shop", snap.ID, "--yes")
	if got := h.get(st.Sites[0].URL+"/", st.CA.CertPath); got != "1" {
		t.Errorf("after restoring Covo's snapshot = %q, want 1 (the empty table, plus this request)", got)
	}

	// .test resolves through Benbow now.
	b, err := os.ReadFile(filepath.Join(h.resolverDir, "test"))
	if err != nil || !strings.Contains(string(b), "# benbow-home: "+h.home()) {
		t.Errorf("resolver file: %v\n%s", err, b)
	}
	if env := migrate(); env.OK || env.Error == nil || env.Error.Code != "not_found" {
		t.Errorf("a second migration = %+v", env.Error)
	}
}
