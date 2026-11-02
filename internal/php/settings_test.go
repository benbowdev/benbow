// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package php

import (
	"strings"
	"testing"

	"github.com/getcovo/covo/internal/errcode"
)

func TestSettingsSet(t *testing.T) {
	tests := []struct {
		name, value string
		ok          bool
	}{
		{"memory_limit", "1g", true},
		{"memory_limit", "-1", true},
		{"memory_limit", "512", false},
		{"memory_limit", "0M", false},
		{"memory_limit", "512M\nextension=evil.so", false},
		{"upload_max_filesize", "64M", true},
		{"upload_max_filesize", "-1", false},
		{"max_execution_time", "0", true},
		{"max_execution_time", "90000", false},
		{"max_execution_time", "ten", false},
		{"display_errors", "Off", true},
		{"display_errors", "maybe", false},
		{"short_open_tag", "On", false},
	}
	for _, tt := range tests {
		t.Run(tt.name+"="+tt.value, func(t *testing.T) {
			s := DefaultSettings()
			err := s.Set(tt.name, tt.value)
			if tt.ok != (err == nil) {
				t.Fatalf("err = %v, want ok=%v", err, tt.ok)
			}
			if err != nil && errcode.Code(err) != errcode.InvalidInput {
				t.Fatalf("code = %s, want invalid_input", errcode.Code(err))
			}
		})
	}
}

func TestINIUsesSettings(t *testing.T) {
	ini, err := INI(INIConfig{
		ErrorLog: "/x/l", SessionDir: "/x/s", Timezone: "UTC", CAFile: "/x/ca.pem",
		Settings: Settings{MemoryLimit: "1G", UploadMaxFilesize: "64M", MaxExecutionTime: 0, DisplayErrors: false},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"memory_limit = 1G\n", "upload_max_filesize = 64M\n", "post_max_size = 64M\n",
		"max_execution_time = 0\n", "display_errors = Off\n", "display_startup_errors = Off\n",
	} {
		if !strings.Contains(ini, want) {
			t.Errorf("php.ini lacks %q", want)
		}
	}
	if _, err := INI(INIConfig{ErrorLog: "/x/l", SessionDir: "/x/s", Timezone: "UTC", CAFile: "/x/ca.pem",
		Settings: Settings{MemoryLimit: "1G\nx=y", UploadMaxFilesize: "64M"}}); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("unsafe stored value err = %v, want invalid_input", err)
	}
}

func TestOverridesAndPHPValue(t *testing.T) {
	s, err := DefaultSettings().WithOverrides(map[string]string{"memory_limit": "1g", "display_errors": "off"})
	if err != nil {
		t.Fatal(err)
	}
	want := "memory_limit=1G\nupload_max_filesize=128M\npost_max_size=128M\nmax_execution_time=300\n" +
		"display_errors=Off\ndisplay_startup_errors=Off\nerror_log=/x/php-error.log"
	if got := s.PHPValue("/x/php-error.log"); got != want {
		t.Fatalf("PHPValue:\n%s\nwant:\n%s", got, want)
	}
	if err := CheckOverrides(map[string]string{"memory_limit": "lots"}); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("bad override err = %v", err)
	}
}

func TestINISendmail(t *testing.T) {
	ini, err := INI(INIConfig{ErrorLog: "/x/l", SessionDir: "/x/s", Timezone: "UTC", CAFile: "/x/ca.pem",
		Sendmail: "/Users/a/Library/Application Support/covo/components/mailpit/mailpit", SendmailPort: 1025})
	if err != nil {
		t.Fatal(err)
	}
	if want := `sendmail_path = "'/Users/a/Library/Application Support/covo/components/mailpit/mailpit' sendmail -S 127.0.0.1:1025"`; !strings.Contains(ini, want) {
		t.Fatalf("php.ini lacks %s", want)
	}
	if _, err := INI(INIConfig{ErrorLog: "/x/l", SessionDir: "/x/s", Timezone: "UTC", CAFile: "/x/ca.pem",
		Sendmail: "/x/it's/mailpit", SendmailPort: 1025}); errcode.Code(err) != errcode.InvalidInput {
		t.Fatalf("quote in sendmail path err = %v", err)
	}
}
