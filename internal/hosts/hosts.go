// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package hosts edits Covo's block in /etc/hosts and nothing else. The block
// sits between two marker lines; everything outside it is preserved byte for
// byte. Every name gets a 127.0.0.1 and a ::1 line (both are needed: with an
// IPv4 line only, macOS may wait on mDNS for the IPv6 answer).
//
// This package is used by the privileged helper: it has no dependencies
// beyond the standard library and validates everything it is given.
package hosts

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Markers delimit Covo's block.
const (
	Begin = "# BEGIN Covo: managed automatically, do not edit"
	End   = "# END Covo"
)

const (
	// MaxNames caps the names in the block.
	MaxNames = 500
	// MaxSize caps the hosts file the helper reads and writes.
	MaxSize = 1 << 20
)

// nameRe is a lowercase name under .local, with at least one label before
// it. No wildcards, no trailing dot.
var nameRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+local$`)

// ErrMarkers means the markers are malformed; nothing is written then.
var ErrMarkers = errors.New("malformed Covo markers in the hosts file")

// ValidName reports whether name may be written in the block.
func ValidName(name string) bool {
	return len(name) <= 253 && nameRe.MatchString(name)
}

// Normalize validates, sorts and de-duplicates names.
func Normalize(names []string) ([]string, error) {
	if len(names) > MaxNames {
		return nil, fmt.Errorf("too many names: %d, at most %d", len(names), MaxNames)
	}
	out := make([]string, 0, len(names))
	for _, n := range names {
		if !ValidName(n) {
			return nil, fmt.Errorf("invalid name %q: only lowercase names ending in .local are allowed", n)
		}
		out = append(out, n)
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// find returns the byte range of the block, line endings included, or
// ok=false if there is none.
func find(content string) (start, end int, ok bool, err error) {
	var begins, ends []int
	off := 0
	for line := range strings.Lines(content) {
		switch strings.TrimRight(line, "\r\n") {
		case Begin:
			begins = append(begins, off)
		case End:
			ends = append(ends, off+len(line))
		}
		off += len(line)
	}
	switch {
	case len(begins) == 0 && len(ends) == 0:
		return 0, 0, false, nil
	case len(begins) == 1 && len(ends) == 1 && begins[0] < ends[0]:
		return begins[0], ends[0], true, nil
	default:
		return 0, 0, false, ErrMarkers
	}
}

// Names returns the names in Covo's block.
func Names(content string) ([]string, error) {
	start, end, ok, err := find(content)
	if err != nil || !ok {
		return []string{}, err
	}
	var names []string
	for line := range strings.Lines(content[start:end]) {
		f := strings.Fields(line)
		if len(f) == 2 && f[0] == "127.0.0.1" {
			names = append(names, f[1])
		}
	}
	return names, nil
}

// block renders Covo's block for names, which must be normalized.
func block(names []string) string {
	var b strings.Builder
	b.WriteString(Begin + "\n")
	for _, n := range names {
		fmt.Fprintf(&b, "127.0.0.1\t%s\n::1\t\t%s\n", n, n)
	}
	b.WriteString(End + "\n")
	return b.String()
}

// Sync returns content with Covo's block holding exactly names. With no
// names the block is removed. Without a block, it is appended at the end.
// Clearing a block that Sync added restores the original content exactly:
// a file without a final newline gets the block after a newline and without
// one of its own, which tells removal to take that newline away again.
func Sync(content string, names []string) (string, error) {
	names, err := Normalize(names)
	if err != nil {
		return "", err
	}
	start, end, ok, err := find(content)
	if err != nil {
		return "", err
	}
	if !ok {
		if len(names) == 0 {
			return content, nil
		}
		if content != "" && !strings.HasSuffix(content, "\n") {
			return content + "\n" + strings.TrimSuffix(block(names), "\n"), nil
		}
		return content + block(names), nil
	}
	// The block was added to a file that had no final newline.
	bare := end == len(content) && !strings.HasSuffix(content, "\n")
	if len(names) == 0 {
		if bare && start > 0 && content[start-1] == '\n' {
			return content[:start-1], nil
		}
		return content[:start] + content[end:], nil
	}
	b := block(names)
	if bare {
		b = strings.TrimSuffix(b, "\n")
	}
	return content[:start] + b + content[end:], nil
}
