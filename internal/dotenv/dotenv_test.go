// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package dotenv

import "testing"

const example = `APP_NAME=Laravel
APP_URL=http://localhost

DB_CONNECTION=sqlite
# DB_HOST=127.0.0.1
# DB_PORT=3306
# DB_DATABASE=laravel

MAIL_MAILER=log
`

func TestSet(t *testing.T) {
	got := Set(example, []Pair{
		{"APP_URL", "https://blog.localhost:8443"},
		{"DB_CONNECTION", "mysql"},
		{"DB_HOST", "127.0.0.1"},
		{"DB_DATABASE", "blog"},
		{"DB_PASSWORD", `p"a ss$1`},
		{"bad-key", "x"},
	})
	want := `APP_NAME=Laravel
APP_URL=https://blog.localhost:8443

DB_CONNECTION=mysql
DB_HOST=127.0.0.1
# DB_PORT=3306
DB_DATABASE=blog

MAIL_MAILER=log
DB_PASSWORD="p\"a ss\$1"
`
	if got != want {
		t.Fatalf("Set =\n%s\nwant\n%s", got, want)
	}
	for key, want := range map[string]string{"DB_PASSWORD": `p"a ss$1`, "APP_URL": "https://blog.localhost:8443"} {
		if v, ok := Get(got, key); !ok || v != want {
			t.Errorf("Get(%s) = %q, %v; want %q", key, v, ok, want)
		}
	}
	if _, ok := Get(got, "DB_PORT"); ok {
		t.Error("Get found a commented-out key")
	}
}
