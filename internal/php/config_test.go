// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package php

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

var update = flag.Bool("update", false, "rewrite golden files")

const home = "/Users/test/Library/Application Support/covo"

func TestGolden(t *testing.T) {
	fpm, err := FPMConf(FPMConfig{
		Minor:    "8.4",
		Socket:   home + "/run/php84.sock",
		PIDFile:  home + "/run/php84.pid",
		ErrorLog: home + "/logs/php-8.4-fpm.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, filepath.Join("testdata", "php-fpm.conf"), fpm)

	ini, err := INI(INIConfig{
		ErrorLog:   home + "/logs/php-8.4-error.log",
		SessionDir: home + "/data/php/8.4/sessions",
		Timezone:   "Europe/Rome",
		CAFile:     home + "/etc/ca-bundle.pem",
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, filepath.Join("testdata", "php.ini"), ini)
	if strings.Contains(ini, "mysqli.") || strings.Contains(ini, "xdebug") {
		t.Error("MySQL or Xdebug settings when both are off")
	}

	ini, err = INI(INIConfig{
		ErrorLog:    home + "/logs/php-8.4-error.log",
		SessionDir:  home + "/data/php/8.4/sessions",
		Timezone:    "Europe/Rome",
		CAFile:      home + "/etc/ca-bundle.pem",
		MySQLSocket: home + "/run/mysql.sock",
		MySQLPort:   3307,
		Xdebug:      home + "/bin/xdebug/3.5.3/8.4.23/xdebug.so",
	})
	if err != nil {
		t.Fatal(err)
	}
	golden(t, filepath.Join("testdata", "php-mysql-xdebug.ini"), ini)
}

func TestRejectsUnsafePaths(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
	}{
		{"fpm env expansion", func() error {
			_, err := FPMConf(FPMConfig{Minor: "8.4", Socket: "/x/${HOME}.sock", PIDFile: "/x/p", ErrorLog: "/x/l"})
			return err
		}},
		{"ini mysql socket", func() error {
			_, err := INI(INIConfig{ErrorLog: "/x/l", SessionDir: "/x/s", Timezone: "UTC", MySQLSocket: "/x/{m}.sock", MySQLPort: 3307})
			return err
		}},
		{"ini quote", func() error {
			_, err := INI(INIConfig{ErrorLog: `/x/"l`, SessionDir: "/x/s", Timezone: "UTC"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.run(); errcode.Code(err) != errcode.InvalidInput {
				t.Fatalf("err = %v, want invalid_input", err)
			}
		})
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
