// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/store"
)

// A snapshot keeps a list of its files (files.tsv next to files/): one line
// per file or symlink with its kind, size and path. A restore checks the
// saved files against it, so a snapshot that lost or changed files is
// refused before the site is touched, and the list says what was saved
// when a restore needs explaining. Sizes, not hashes: hashing every file
// of a big site would make each snapshot slow.

func fileListPath(sn store.Snapshot) string {
	return filepath.Join(filepath.Dir(sn.FilesDir), "files.tsv")
}

type fileEntry struct {
	kind string // "f" file, "l" symlink
	size int64
}

// listFiles walks dir and returns its files and symlinks by relative path.
func listFiles(dir string) (map[string]fileEntry, error) {
	out := map[string]fileEntry{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		kind := "f"
		if d.Type()&fs.ModeSymlink != 0 {
			kind = "l"
		} else if !d.Type().IsRegular() {
			return nil // sockets, pipes: never restored as content
		}
		fi, err := d.Info()
		if err != nil {
			return fmt.Errorf("stat %s: %w", p, err)
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = fileEntry{kind: kind, size: fi.Size()}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", dir, err)
	}
	return out, nil
}

// writeFileList records the files of dir in path.
func writeFileList(dir, path string) error {
	files, err := listFiles(dir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		e := files[n]
		fmt.Fprintf(&b, "%s\t%d\t%s\n", e.kind, e.size, strconv.Quote(n))
	}
	if _, err := fsutil.WriteAtomic(path, []byte(b.String()), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

// checkFileList compares dir with the list in path. Snapshots taken before
// the list existed have none and pass.
func checkFileList(dir, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	want := map[string]fileEntry{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			return fmt.Errorf("its file list %s is damaged", path)
		}
		size, err1 := strconv.ParseInt(parts[1], 10, 64)
		name, err2 := strconv.Unquote(parts[2])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("its file list %s is damaged", path)
		}
		want[name] = fileEntry{kind: parts[0], size: size}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	got, err := listFiles(dir)
	if err != nil {
		return err
	}
	var bad []string
	for n, w := range want {
		if g, ok := got[n]; !ok {
			bad = append(bad, n+" (missing)")
		} else if g != w {
			bad = append(bad, n+" (changed)")
		}
	}
	for n := range got {
		if _, ok := want[n]; !ok {
			bad = append(bad, n+" (not in the snapshot)")
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	more := ""
	if len(bad) > 3 {
		more = fmt.Sprintf(" and %d more", len(bad)-3)
		bad = bad[:3]
	}
	return fmt.Errorf("its saved files do not match its file list: %s%s", strings.Join(bad, ", "), more)
}
