// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/ca"
	"github.com/benbowdev/benbow/internal/caddy"
	"github.com/benbowdev/benbow/internal/php"
)

// TestGeneratedConfigsAreValid feeds generated configs, with spaces in every
// path, to the real caddy and php-fpm binaries.
func TestGeneratedConfigsAreValid(t *testing.T) {
	h := newHarness(t)
	var c struct {
		Binary string `json:"binary"`
	}
	if err := json.Unmarshal(h.mustBenbow("caddy", "install"), &c); err != nil {
		t.Fatal(err)
	}
	var p struct {
		Binary string `json:"binary"`
	}
	if err := json.Unmarshal(h.mustBenbow("php", "install", "8.4"), &p); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(t.TempDir(), "with space")
	// Sockets need a short path (see paths.Layout.Run).
	run, err := os.MkdirTemp("", "benbow e2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	for _, d := range []string{"logs", "data/caddy", "site root"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	caDir := ca.Dir(filepath.Join(dir, "data/ca"))
	if _, err := ca.Ensure(caDir, "Benbow Test CA", time.Now()); err != nil {
		t.Fatal(err)
	}
	caddyfile, err := caddy.Caddyfile(caddy.Config{
		AdminSocket: filepath.Join(run, "caddy-admin.sock"),
		HTTPSPort:   h.port,
		StorageDir:  filepath.Join(dir, "data/caddy"),
		LogFile:     filepath.Join(dir, "logs/caddy.log"),
		CA: caddy.CA{
			Name: "Benbow Test CA", RootCert: caDir.RootCert(),
			IntermediateCert: caDir.IntermediateCert(), IntermediateKey: caDir.IntermediateKey(),
		},
		Sites: []caddy.Site{{
			Name: "demo", Domain: "demo.localhost",
			Root: filepath.Join(dir, "site root"), FPMSocket: filepath.Join(run, "php84.sock"),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	fpm, err := php.FPMConf(php.FPMConfig{
		Minor: "8.4", Socket: filepath.Join(run, "php84.sock"),
		PIDFile: filepath.Join(run, "php84.pid"), ErrorLog: filepath.Join(dir, "logs/fpm.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	ini, err := php.INI(php.INIConfig{
		ErrorLog: filepath.Join(dir, "logs/php.log"), SessionDir: filepath.Join(dir, "sessions"), Timezone: "UTC",
		CAFile: caDir.RootCert(),
	})
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	cmd := exec.Command(c.Binary, "validate", "--config", write("Caddyfile", caddyfile), "--adapter", "caddyfile")
	cmd.Env = append(os.Environ(), "XDG_DATA_HOME="+filepath.Join(dir, "data"), "XDG_CONFIG_HOME="+filepath.Join(dir, "data"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("caddy validate: %v\n%s", err, out)
	}
	cmd = exec.Command(p.Binary, "-t", "-c", write("php.ini", ini), "-y", write("php-fpm.conf", fpm))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("php-fpm -t: %v\n%s", err, out)
	}
}
