// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// DiskPart is one line of DiskReport: what it is and the space it takes.
type DiskPart struct {
	// Name is stable (php, servers, databases, snapshots, caches, logs,
	// other); Title is for people.
	Name  string `json:"name"`
	Title string `json:"title"`
	Bytes int64  `json:"bytes"`
	// Parts break it down (each PHP version, each database server).
	Parts []DiskPart `json:"parts,omitempty"`
}

// DiskReport is the disk space Benbow's home takes.
type DiskReport struct {
	Home  string     `json:"home"`
	Total int64      `json:"total"`
	Parts []DiskPart `json:"parts"`
	// SnapshotsShared is the part of the snapshots' files still shared
	// with the sites (APFS clones): not counted in Total.
	SnapshotsShared int64 `json:"snapshots_shared"`
}

// DiskUsage measures Benbow's home. Snapshots count only what deleting
// them would give back; their files are mostly APFS clones of the sites.
func (c *Core) DiskUsage(ctx context.Context) (DiskReport, error) {
	root := c.Layout.Root
	bin := c.Installer.BinDir
	data := c.Layout.Data
	rep := DiskReport{Home: root}

	var mu sync.Mutex
	var wg sync.WaitGroup
	sizes := map[string]int64{}
	measure := func(paths ...string) {
		for _, p := range paths {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n := dirSize(p)
				mu.Lock()
				sizes[p] = n
				mu.Unlock()
			}()
		}
	}
	// Everything at the top of the home and of bin/ and data/: each byte
	// is counted once, under the line its folder belongs to.
	children := func(dir string) []string {
		entries, _ := os.ReadDir(dir)
		var out []string
		for _, e := range entries {
			out = append(out, filepath.Join(dir, e.Name()))
		}
		return out
	}
	var top []string
	for _, p := range children(root) {
		if p != bin && p != data {
			top = append(top, p)
		}
	}
	binDirs, dataDirs := children(bin), children(data)
	snapshotsDir := filepath.Join(data, "snapshots")
	measure(top...)
	measure(binDirs...)
	for _, p := range dataDirs {
		if p != snapshotsDir {
			measure(p)
		}
	}
	var phpParts []DiskPart
	phpMinor := map[string]int64{}
	for _, comp := range []string{"php", "php-cli"} {
		for _, v := range children(filepath.Join(bin, comp)) {
			measure(v)
		}
	}
	snaps, snapErr := c.SnapshotUsage(ctx, "")
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return rep, fmt.Errorf("measure disk usage: %w", err)
	}

	add := func(name, title string, bytes int64, parts []DiskPart) {
		rep.Parts = append(rep.Parts, DiskPart{Name: name, Title: title, Bytes: bytes, Parts: parts})
		rep.Total += bytes
	}

	// PHP: FPM and CLI builds and Xdebug, per minor version.
	var phpTotal int64
	for _, comp := range []string{"php", "php-cli"} {
		for _, v := range children(filepath.Join(bin, comp)) {
			phpMinor[minorOf(filepath.Base(v))] += sizes[v]
		}
	}
	for _, x := range children(filepath.Join(bin, "xdebug")) {
		// bin/xdebug/<xdebug version>/<php version>
		for _, v := range children(x) {
			phpMinor[minorOf(filepath.Base(v))] += dirSize(v)
		}
	}
	for minor, n := range phpMinor {
		phpParts = append(phpParts, DiskPart{Name: "php-" + minor, Title: "PHP " + minor, Bytes: n})
		phpTotal += n
	}
	sort.Slice(phpParts, func(i, j int) bool { return phpParts[i].Name > phpParts[j].Name })
	add("php", "PHP versions", phpTotal, phpParts)

	var servers int64
	for _, p := range binDirs {
		switch filepath.Base(p) {
		case "php", "php-cli", "xdebug":
		default:
			servers += sizes[p]
		}
	}
	add("servers", "Servers and tools", servers, nil)

	var dbTotal int64
	var dbParts []DiskPart
	dbNames := map[string]string{"mysql": "MySQL 8.4", "mysql57": "MySQL 5.7", "postgres": "PostgreSQL", "redis": "Redis", "mailpit": "Mail"}
	caches := map[string]bool{"wp-cli": true, "composer": true}
	var cacheTotal, other int64
	for _, p := range dataDirs {
		base := filepath.Base(p)
		switch {
		case p == snapshotsDir:
		case dbNames[base] != "":
			dbParts = append(dbParts, DiskPart{Name: base, Title: dbNames[base], Bytes: sizes[p]})
			dbTotal += sizes[p]
		case caches[base]:
			cacheTotal += sizes[p]
		default:
			other += sizes[p]
		}
	}
	add("databases", "Databases and mail", dbTotal, dbParts)

	if snapErr == nil {
		add("snapshots", "Snapshots", snaps.Private, nil)
		if snaps.Apparent > snaps.Private {
			rep.SnapshotsShared = snaps.Apparent - snaps.Private
		}
	}

	var logs int64
	for _, p := range top {
		switch filepath.Base(p) {
		case "logs":
			logs += sizes[p]
		case "tmp":
			cacheTotal += sizes[p]
		default:
			other += sizes[p]
		}
	}
	add("caches", "Downloads and caches", cacheTotal, nil)
	add("logs", "Logs", logs, nil)
	add("other", "Settings and backups", other, nil)
	return rep, nil
}
