// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package wpconfig

import (
	"strings"
	"testing"
)

const sample = `<?php
// ** MySQL settings ** //
// define('DB_NAME', 'commented');
define( 'DB_NAME', 'shop_db' );
define("DB_USER", "root");
define('DB_PASSWORD', 'r\'oot');
define( 'DB_HOST', 'localhost:/Applications/MAMP/tmp/mysql/mysql.sock' );
define( 'DB_CHARSET', 'utf8mb4' );
$table_prefix = 'sh_';
`

func TestRead(t *testing.T) {
	cfg := Read(sample)
	want := map[string]string{"DB_NAME": "shop_db", "DB_USER": "root", "DB_PASSWORD": "r'oot", "DB_HOST": "localhost:/Applications/MAMP/tmp/mysql/mysql.sock"}
	for k, v := range want {
		if cfg.Values[k] != v {
			t.Errorf("%s = %q, want %q", k, cfg.Values[k], v)
		}
	}
	if cfg.TablePrefix != "sh_" {
		t.Errorf("prefix = %q", cfg.TablePrefix)
	}
}

func TestSet(t *testing.T) {
	out, err := Set(sample, map[string]string{"DB_NAME": "shop", "DB_USER": "shop", "DB_PASSWORD": `a'b"c$d`, "DB_HOST": "127.0.0.1:3307"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// define('DB_NAME', 'commented');",
		"define( 'DB_NAME', 'shop' );",
		`define("DB_USER", "shop");`,
		`define('DB_PASSWORD', 'a\'b"c$d');`,
		"define( 'DB_HOST', '127.0.0.1:3307' );",
		"define( 'DB_CHARSET', 'utf8mb4' );",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("result lacks %q:\n%s", want, out)
		}
	}
	if got := Read(out).Values["DB_PASSWORD"]; got != `a'b"c$d` {
		t.Errorf("password read back = %q", got)
	}
	if strings.Count(out, "\n") != strings.Count(sample, "\n") {
		t.Error("line count changed")
	}
	if _, err := Set("<?php define('DB_NAME', getenv('X'));", map[string]string{"DB_NAME": "x"}); err == nil {
		t.Fatal("a define from code was replaced")
	}
}
