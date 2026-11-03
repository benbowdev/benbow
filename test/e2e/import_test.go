// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type importPlan struct {
	DryRun     bool `json:"dry_run"`
	Create     int  `json:"create"`
	Skip       int  `json:"skip"`
	NeedsLocal bool `json:"needs_local"`
	Applied    bool `json:"applied"`
	Items      []struct {
		MAMP struct {
			ServerName string `json:"server_name"`
		} `json:"mamp"`
		Action   string   `json:"action"`
		Reason   string   `json:"reason"`
		Name     string   `json:"name"`
		Domain   string   `json:"domain"`
		PHP      string   `json:"php"`
		Aliases  []string `json:"aliases"`
		Warnings []string `json:"warnings"`
	} `json:"items"`
	Databases []string `json:"databases"`
}

// mampFixture writes the synthetic MAMP PRO settings with its folders under
// a temporary root and points Covo at it.
func mampFixture(t *testing.T, h *harness) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Sites")
	for _, d := range []string{"htdocs", "Shop Site/public", "blog", "intranet", "shop"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "index.php"), []byte("<?php echo '"+d+" '.PHP_MAJOR_VERSION.'.'.PHP_MINOR_VERSION;"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	body, err := os.ReadFile("../../internal/mamp/testdata/settings6.xml")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "settings6.plist")
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(body), "/Users/test/Sites", root)), 0o444); err != nil {
		t.Fatal(err)
	}
	h.env = append(h.env, "COVO_MAMP_SETTINGS="+path)
	return path
}

func fileSum(t *testing.T, path string) [32]byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(b)
}

func (h *harness) importMAMP(args ...string) importPlan {
	h.t.Helper()
	var plan importPlan
	if err := json.Unmarshal(h.mustCovo(append([]string{"import", "mamp"}, args...)...), &plan); err != nil {
		h.t.Fatal(err)
	}
	return plan
}

func (h *harness) siteCount() int {
	h.t.Helper()
	var sites []json.RawMessage
	if err := json.Unmarshal(h.mustCovo("site", "list"), &sites); err != nil {
		h.t.Fatal(err)
	}
	return len(sites)
}

func TestImportMAMP(t *testing.T) {
	h := newHarness(t)
	src := mampFixture(t, h)
	sum := fileSum(t, src)

	// Dry run: the plan, nothing created.
	plan := h.importMAMP("--dry-run")
	if !plan.DryRun || plan.Create != 3 || plan.Skip != 3 || !plan.NeedsLocal {
		t.Fatalf("dry run = %+v", plan)
	}
	// PHP 7.4 exists on Apple Silicon (covo-binaries' legacy builds);
	// elsewhere it maps to 8.1 with a warning.
	legacy := "8.1"
	if runtime.GOARCH == "arm64" {
		legacy = "7.4"
	}
	want := map[string]struct{ action, domain, php string }{
		"localhost":         {"skip", "", ""},
		"shop.local":        {"create", "shop.local", "8.1"},
		"blog.client.local": {"create", "blog.client.local", legacy}, // PHP 7.4
		"intranet":          {"create", "intranet.local", "8.2"},
		"gone.local":        {"skip", "", ""},
		"Bad_Name.local":    {"skip", "", ""},
	}
	for _, it := range plan.Items {
		w := want[it.MAMP.ServerName]
		if it.Action != w.action || it.Domain != w.domain || it.PHP != w.php {
			t.Errorf("%s: %+v, want %+v", it.MAMP.ServerName, it, w)
		}
		if it.MAMP.ServerName == "blog.client.local" && legacy == "8.1" && !strings.Contains(strings.Join(it.Warnings, "|"), "PHP 7.4.33 is not available") {
			t.Errorf("no PHP warning: %v", it.Warnings)
		}
		if it.MAMP.ServerName == "blog.client.local" && strings.Join(it.Aliases, ",") != "www.blog.client.local" {
			t.Errorf("blog aliases = %v", it.Aliases)
		}
	}
	if strings.Join(plan.Databases, ",") != "blog,blog_old,shop" {
		t.Errorf("databases = %v", plan.Databases)
	}
	if n := h.siteCount(); n != 0 {
		t.Fatalf("dry run created %d sites", n)
	}

	// .local names without the compatibility mode: refused, nothing created.
	if env := h.covo("import", "mamp"); env.OK || env.Error.Code != "confirmation_required" {
		t.Fatalf("import without .local: %+v", env)
	}
	if n := h.siteCount(); n != 0 {
		t.Fatalf("refused import created %d sites", n)
	}

	// --localhost: created and served.
	h.mustCovo("start")
	plan = h.importMAMP("--localhost")
	if plan.Create != 3 || !plan.Applied {
		t.Fatalf("import = %+v", plan)
	}
	var status struct {
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
		Sites []struct {
			Name    string   `json:"name"`
			URL     string   `json:"url"`
			Aliases []string `json:"aliases"`
		} `json:"sites"`
	}
	if err := json.Unmarshal(h.mustCovo("status"), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Sites) != 3 {
		t.Fatalf("sites = %+v", status.Sites)
	}
	for _, s := range status.Sites {
		body := h.get(s.URL, status.CA.CertPath)
		wantBody := map[string]string{"shop": "Shop Site/public 8.1", "blog-client": "blog " + legacy, "intranet": "intranet 8.2"}[s.Name]
		if body != wantBody {
			t.Errorf("%s: %q, want %q", s.URL, body, wantBody)
		}
		if s.Name == "blog-client" && strings.Join(s.Aliases, ",") != "www.blog.client.localhost" {
			t.Errorf("blog-client aliases = %v", s.Aliases)
		}
	}

	// Again: everything is already there.
	if plan = h.importMAMP("--localhost"); plan.Create != 0 {
		t.Fatalf("second import = %+v", plan)
	}
	if fileSum(t, src) != sum {
		t.Fatal("MAMP settings changed")
	}
}

func TestImportMAMPKeepsLocalNames(t *testing.T) {
	h := newHarness(t)
	mampFixture(t, h)
	h.mustCovo("domains", "enable", "local")
	plan := h.importMAMP()
	if plan.Create != 3 || plan.NeedsLocal {
		t.Fatalf("import = %+v", plan)
	}
	hosts := h.hosts()
	for _, d := range []string{"shop.local", "blog.client.local", "intranet.local"} {
		if !strings.Contains(hosts, "127.0.0.1\t"+d+"\n") || !strings.Contains(hosts, "::1\t\t"+d+"\n") {
			t.Errorf("%s not in the hosts block:\n%s", d, hosts)
		}
	}
	if !strings.HasPrefix(hosts, h.hostsOriginal) {
		t.Error("lines outside Covo's block changed")
	}
}

// TestImportMAMPLinksDatabases links a site to its MAMP database when that
// database was already copied to Covo's MySQL.
func TestImportMAMPLinksDatabases(t *testing.T) {
	if runtime.GOARCH != "arm64" {
		t.Skip("MySQL 8.4 is pinned for Apple Silicon only")
	}
	h := newHarness(t)
	mampFixture(t, h)
	h.mustCovo("mysql", "enable")
	h.mustCovo("start")
	// A database "shop" on Covo's MySQL, as after a mysqldump copy.
	h.mustCovo("site", "add", "tmp", "--path", newSite(t))
	h.mustCovo("db", "create", "tmp", "--name", "shop")
	h.mustCovo("db", "unlink", "tmp")
	h.mustCovo("site", "remove", "tmp")

	var plan struct {
		Items []struct {
			Name     string `json:"name"`
			Database string `json:"database"`
		} `json:"items"`
	}
	if err := json.Unmarshal(h.mustCovo("import", "mamp", "--localhost"), &plan); err != nil {
		t.Fatal(err)
	}
	linked := map[string]string{}
	for _, it := range plan.Items {
		if it.Database != "" {
			linked[it.Name] = it.Database
		}
	}
	if len(linked) != 1 || linked["shop"] != "shop" {
		t.Fatalf("linked = %v (blog has two MAMP databases and none on Covo)", linked)
	}
	var db struct {
		Database string `json:"database"`
		User     string `json:"user"`
	}
	if err := json.Unmarshal(h.mustCovo("db", "show", "shop"), &db); err != nil || db.Database != "shop" || db.User != "root" {
		t.Fatalf("db show shop = %+v, %v", db, err)
	}
}
