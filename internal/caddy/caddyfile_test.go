// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package caddy

import (
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

var update = flag.Bool("update", false, "rewrite golden files")

const home = "/Users/test/Library/Application Support/covo"

var testCA = CA{
	Name:             "Covo Local CA",
	RootCert:         home + "/data/ca/root.crt",
	IntermediateCert: home + "/data/ca/intermediate.crt",
	IntermediateKey:  home + "/data/ca/intermediate.key",
}

func TestCaddyfileGolden(t *testing.T) {
	tests := []struct {
		name    string
		sites   []Site
		adminer *Adminer
		phone   *Phone
	}{
		{"no-sites", nil, nil, nil},
		{"two-sites", []Site{
			{Name: "demo", Domain: "demo.localhost", Aliases: []string{"shop.test", "www.demo.localhost"}, Root: "/Users/test/Sites/My Demo", FPMSocket: home + "/run/php84.sock",
				AccessLog: home + "/logs/sites/demo/access.log",
				PHPValue:  "memory_limit=1G\ndisplay_errors=Off\nerror_log=" + home + "/logs/sites/demo/php-error.log",
				ProbeDir:  home + "/tmp/probes", ProbePHPValue: "memory_limit=1G\ndisplay_errors=Off"},
			{Name: "legacy", Domain: "legacy.localhost", Root: "/Users/test/Sites/legacy", FPMSocket: home + "/run/php82.sock"},
		}, &Adminer{Dir: home + "/components/adminer/6.2.0-all", FPMSocket: home + "/run/php84.sock"},
			&Phone{Site: "demo", Host: "demo-mac.local", Port: 9443, Binds: []string{"192.168.1.5", "10.0.0.2"},
				Root: "/Users/test/Sites/My Demo", FPMSocket: home + "/run/php84.sock",
				PHPValue: "memory_limit=1G\nauto_prepend_file=" + home + "/data/phone/prepend.php",
				LocalURL: "https://demo.localhost", PhoneURL: "https://demo-mac.local:9443", CAPort: 9444, CADir: home + "/data/phone/ca",
				PlainPort: 9445, PlainURL: "http://192.168.1.5:9445"}},
		{"apps", []Site{
			{Name: "shop", Domain: "shop.localhost", Aliases: []string{"www.shop.localhost"}, Upstream: 41000,
				AccessLog: home + "/logs/sites/shop/access.log"},
			{Name: "api", Domain: "api.covo.test", Upstream: 3000, UpstreamLocalHost: true},
		}, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Caddyfile(Config{
				AdminSocket: home + "/run/caddy-admin.sock",
				HTTPSPort:   8443,
				StorageDir:  home + "/data/caddy",
				LogFile:     home + "/logs/caddy.log",
				CA:          testCA,
				Sites:       tt.sites,
				Adminer:     tt.adminer,
				MailUIPort:  map[bool]int{true: 8025}[tt.adminer != nil],
				// 443 and 80 forwarded to 8443 and 8080.
				HTTPPort:    map[bool]int{true: 8080}[tt.adminer != nil],
				PublicHTTPS: map[bool]int{true: 443}[tt.adminer != nil],
				Phone:       tt.phone,
			})
			if err != nil {
				t.Fatal(err)
			}
			golden(t, filepath.Join("testdata", tt.name+".Caddyfile"), got)
		})
	}
}

func TestCaddyfileRejectsPublicPhoneAddress(t *testing.T) {
	_, err := Caddyfile(Config{
		AdminSocket: home + "/run/caddy-admin.sock", StorageDir: home + "/data/caddy", LogFile: home + "/logs/caddy.log", CA: testCA,
		Phone: &Phone{Site: "x", Host: "x-mac.local", Port: 9443, Binds: []string{"8.8.8.8"}, Root: "/Users/test/x",
			FPMSocket: home + "/run/php84.sock", PHPValue: "memory_limit=1G", LocalURL: "https://x.localhost", PhoneURL: "https://x-mac.local:9443", CADir: "/x"},
	})
	if errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("public address err = %v", err)
	}
}

func TestRedirectPort(t *testing.T) {
	for _, tt := range []struct {
		cfg  Config
		want string
	}{
		{Config{HTTPSPort: 8443, PublicHTTPS: 443}, ""},
		{Config{HTTPSPort: 8443}, ":8443"},
		{Config{HTTPSPort: 9443, PublicHTTPS: 9443}, ":9443"},
	} {
		if got := tt.cfg.RedirectPort(); got != tt.want {
			t.Errorf("RedirectPort(%+v) = %q, want %q", tt.cfg, got, tt.want)
		}
	}
}

func TestCaddyfileRejectsUnsafePaths(t *testing.T) {
	_, err := Caddyfile(Config{
		AdminSocket: home + "/run/caddy-admin.sock",
		StorageDir:  home + "/data/caddy",
		LogFile:     home + "/logs/caddy.log",
		CA:          testCA,
		Sites:       []Site{{Name: "x", Domain: "x.localhost", Root: "/Users/test/{env.HOME}", FPMSocket: home + "/run/php84.sock"}},
	})
	if errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("err = %v, want invalid_input", err)
	}
}

func TestCaddyfileRejectsBadDomains(t *testing.T) {
	_, err := Caddyfile(Config{
		AdminSocket: home + "/run/caddy-admin.sock",
		StorageDir:  home + "/data/caddy",
		LogFile:     home + "/logs/caddy.log",
		CA:          testCA,
		Sites: []Site{{Name: "x", Domain: "x.localhost", Aliases: []string{`a.localhost" {`},
			Root: "/Users/test/x", FPMSocket: home + "/run/php84.sock"}},
	})
	if errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("err = %v, want invalid_input", err)
	}
	_, err = Caddyfile(Config{
		AdminSocket: home + "/run/caddy-admin.sock", StorageDir: home + "/data/caddy", LogFile: home + "/logs/caddy.log", CA: testCA,
		Sites: []Site{{Name: "x", Domain: "x.localhost", Root: "/Users/test/x", FPMSocket: home + "/run/php84.sock",
			PHPValue: "memory_limit=1G\"\n}"}},
	})
	if errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("bad PHP value err = %v, want invalid_input", err)
	}
}

func golden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("%s mismatch:\n--- got\n%s\n--- want\n%s", path, got, want)
	}
}
