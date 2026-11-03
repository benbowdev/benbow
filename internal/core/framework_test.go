// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectFramework(t *testing.T) {
	root := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	laravel := `{"require": {"php": "^8.2", "laravel/framework": "^12.0"}}`
	write("app/artisan", "#!/usr/bin/env php")
	write("app/composer.json", laravel)
	write("app/public/index.php", "<?php")
	write("lumen/artisan", "")
	write("lumen/composer.json", `{"require": {"laravel/lumen-framework": "^10"}}`)
	write("lumen/public/index.php", "<?php")
	write("noPublic/artisan", "")
	write("noPublic/composer.json", laravel)
	write("wp/wp-includes/version.php", "<?php")
	write("plain/index.php", "<?php")

	for _, tt := range []struct {
		dir, docroot, appRoot, framework string
	}{
		{"app", "app/public", "app", FrameworkLaravel},
		{"app/public", "app/public", "app", FrameworkLaravel},
		{"lumen", "lumen", "", ""},
		{"noPublic", "noPublic", "", ""},
		{"wp", "wp", "", FrameworkWordPress},
		{"plain", "plain", "", ""},
	} {
		docroot, appRoot, fw := detectFramework(filepath.Join(root, tt.dir))
		wantApp := ""
		if tt.appRoot != "" {
			wantApp = filepath.Join(root, tt.appRoot)
		}
		if docroot != filepath.Join(root, tt.docroot) || appRoot != wantApp || fw != tt.framework {
			t.Errorf("%s: got %q %q %q", tt.dir, docroot, appRoot, fw)
		}
	}
}

func TestComposerFor(t *testing.T) {
	for minor, want := range map[string]string{"5.6": "composer-lts", "7.2": "composer-lts", "7.3": "composer", "8.4": "composer"} {
		if got := composerFor(minor); got != want {
			t.Errorf("composerFor(%s) = %s, want %s", minor, got, want)
		}
	}
}
