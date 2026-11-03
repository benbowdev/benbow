// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package dotenv changes keys in a .env file and leaves every other line,
// comment and blank line as it was.
package dotenv

import (
	"regexp"
	"strings"
)

// Pair is one key and its new value.
type Pair struct {
	Key, Value string
}

var keyRe = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// Set returns content with each key set to its value: the first line that
// sets the key (also a commented-out one, as Laravel's .env.example has for
// DB_HOST) is replaced; keys not found are appended in order. Invalid keys
// are skipped.
func Set(content string, pairs []Pair) string {
	lines := strings.Split(content, "\n")
	for _, p := range pairs {
		if !keyRe.MatchString(p.Key) {
			continue
		}
		line := p.Key + "=" + quote(p.Value)
		re := regexp.MustCompile(`^\s*#?\s*` + p.Key + `\s*=`)
		done := false
		for i, l := range lines {
			if re.MatchString(l) {
				lines[i] = line
				done = true
				break
			}
		}
		if !done {
			if n := len(lines); n > 0 && lines[n-1] == "" {
				lines = append(lines[:n-1], line, "")
			} else {
				lines = append(lines, line)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// Get returns the value of key (the first uncommented line), unquoted.
func Get(content, key string) (string, bool) {
	re := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(key) + `\s*=(.*)$`)
	for _, l := range strings.Split(content, "\n") {
		if m := re.FindStringSubmatch(l); m != nil {
			v := strings.TrimSpace(m[1])
			if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
				dq := v[0] == '"'
				v = v[1 : len(v)-1]
				if dq {
					v = strings.NewReplacer(`\"`, `"`, `\$`, "$", `\n`, "\n", `\\`, `\`).Replace(v)
				}
			}
			return v, true
		}
	}
	return "", false
}

// quote leaves simple values bare and double-quotes the rest.
func quote(v string) string {
	if v != "" && !strings.ContainsAny(v, " \t#\"'\\$`=\n") {
		return v
	}
	v = strings.ReplaceAll(v, `\`, `\\`)
	v = strings.ReplaceAll(v, `"`, `\"`)
	v = strings.ReplaceAll(v, "$", `\$`)
	v = strings.ReplaceAll(v, "\n", `\n`)
	return `"` + v + `"`
}
