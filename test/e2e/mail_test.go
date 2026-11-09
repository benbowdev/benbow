// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMail sends mail from PHP and finds it in Mailpit through its API.
func TestMail(t *testing.T) {
	h := newHarness(t)
	dir := newSite(t)
	page := `<?php echo mail('client@example.com', 'Benbow test ' . $_GET['n'], 'Hello from Benbow') ? 'sent' : 'failed';`
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	h.mustBenbow("site", "add", "shop", "--php", "8.4", "--path", dir)

	var res struct {
		Enabled  bool   `json:"enabled"`
		Running  bool   `json:"running"`
		SMTPPort int    `json:"smtp_port"`
		UIPort   int    `json:"ui_port"`
		URL      string `json:"url"`
		Version  string `json:"version"`
	}
	if err := json.Unmarshal(h.mustBenbow("mail", "enable"), &res); err != nil {
		t.Fatal(err)
	}
	if !res.Enabled || res.Running || res.SMTPPort < 1025 || res.UIPort < 8025 || res.Version != "1.33.0" {
		t.Fatalf("enable while stopped = %+v", res)
	}
	h.mustBenbow("start")
	st := h.status()
	if got := h.get(st.Sites[0].URL+"/?n=1", st.CA.CertPath); got != "sent" {
		t.Fatalf("mail() = %q", got)
	}
	var found string
	for range 50 {
		var list struct {
			Messages []struct {
				Subject string `json:"Subject"`
			} `json:"messages"`
		}
		if err := json.Unmarshal([]byte(h.get(res.URL+"api/v1/messages", st.CA.CertPath)), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Messages) > 0 {
			found = list.Messages[0].Subject
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if found != "Benbow test 1" {
		t.Fatalf("Mailpit inbox: %q", found)
	}
	if env := h.benbow("site", "add", "mail", "--path", dir); env.OK || env.Error.Code != "conflict" {
		t.Fatalf("site named mail = %+v", env)
	}

	// Off: Mailpit stops and PHP no longer points at it.
	h.mustBenbow("mail", "disable")
	for _, svc := range h.status().Services {
		if svc.Name == "mailpit" {
			t.Fatal("mailpit still in services after disable")
		}
	}
	ini, err := os.ReadFile(filepath.Join(homeOf(t, h), "etc", "php", "8.4", "php.ini"))
	if err != nil || strings.Contains(string(ini), "sendmail_path") {
		t.Fatalf("php.ini after disable has sendmail_path (%v)", err)
	}
}
