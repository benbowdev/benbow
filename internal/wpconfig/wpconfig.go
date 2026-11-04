// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package wpconfig reads the database settings of a wp-config.php and
// changes them, and nothing else, in place.
package wpconfig

import (
	"regexp"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// Keys Covo reads and changes.
var Keys = []string{"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST"}

// Config is what Covo needs from a wp-config.php.
type Config struct {
	Values map[string]string
	// TablePrefix is $table_prefix ("wp_" when not found).
	TablePrefix string
}

// defineRe matches define('KEY', 'value') or with double quotes: group 1
// is a single-quoted value, group 2 a double-quoted one.
func defineRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`define\(\s*['"]` + key + `['"]\s*,\s*(?:'((?:\\.|[^\\'])*)'|"((?:\\.|[^\\"])*)")\s*\)`)
}

// valueAt returns the value's position in a FindStringSubmatchIndex match.
func valueAt(m []int) (start, end int) {
	if m[2] >= 0 {
		return m[2], m[3]
	}
	return m[4], m[5]
}

var prefixRe = regexp.MustCompile(`\$table_prefix\s*=\s*['"]([A-Za-z0-9_]+)['"]`)

// unquote undoes PHP string escapes of \' \" and \\ .
func unquote(s string) string {
	return strings.NewReplacer(`\\`, `\`, `\'`, `'`, `\"`, `"`).Replace(s)
}

// Read returns the database settings. Keys whose define cannot be read
// (missing, or set from code) are absent.
func Read(content string) Config {
	cfg := Config{Values: map[string]string{}, TablePrefix: "wp_"}
	for _, k := range Keys {
		clean := stripComments(content)
		if m := defineRe(k).FindStringSubmatchIndex(clean); m != nil {
			start, end := valueAt(m)
			cfg.Values[k] = unquote(content[start:end])
		}
	}
	if m := prefixRe.FindStringSubmatch(stripComments(content)); m != nil {
		cfg.TablePrefix = m[1]
	}
	return cfg
}

var commentRe = regexp.MustCompile(`(?m)^\s*(//|#).*$`)

// stripComments blanks whole-line comments, so a commented-out define is
// never taken for the real one. Positions are kept.
func stripComments(s string) string {
	return commentRe.ReplaceAllStringFunc(s, func(m string) string { return strings.Repeat(" ", len(m)) })
}

// Set returns content with the given keys' values replaced in their
// define() lines, keeping each line's quotes. Every key must be found
// exactly once, outside comments: otherwise nothing changes and the error
// says why.
func Set(content string, values map[string]string) (string, error) {
	clean := stripComments(content)
	type edit struct{ start, end int }
	edits := map[string]edit{}
	for k := range values {
		ms := defineRe(k).FindAllStringSubmatchIndex(clean, -1)
		if len(ms) != 1 {
			return "", errcode.Newf(errcode.Unsupported, "wp-config.php: found %d define('%s', …) lines, need exactly one: change it by hand", len(ms), k)
		}
		start, end := valueAt(ms[0])
		edits[k] = edit{start, end} // the value inside the quotes
	}
	out := content
	// Replace from the end so earlier positions stay valid.
	for range values {
		best, bk := -1, ""
		for k, e := range edits {
			if e.start > best {
				best, bk = e.start, k
			}
		}
		e := edits[bk]
		quote := out[e.start-1 : e.start]
		v := strings.ReplaceAll(values[bk], `\`, `\\`)
		v = strings.ReplaceAll(v, quote, `\`+quote)
		if quote == `"` {
			v = strings.ReplaceAll(v, "$", `\$`)
		}
		out = out[:e.start] + v + out[e.end:]
		delete(edits, bk)
	}
	return out, nil
}
