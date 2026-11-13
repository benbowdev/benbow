// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/store"
)

// SnapshotUsage is a snapshot and the space it takes.
type SnapshotUsage struct {
	store.Snapshot
	// Apparent is what du reports (the files are APFS clones of the site,
	// so this is mostly shared); Private is what deleting the snapshot
	// gives back: its database dump plus the files that changed since.
	Apparent int64 `json:"apparent"`
	Private  int64 `json:"private"`
	// Protected snapshots are never pruned: the starting point of an open
	// agent session and its checkpoints.
	Protected bool `json:"protected,omitempty"`
}

// SnapshotsUsage is the space snapshots take, newest first.
type SnapshotsUsage struct {
	Snapshots []SnapshotUsage `json:"snapshots"`
	Apparent  int64           `json:"apparent"`
	Private   int64           `json:"private"`
}

// usageTTL is how long a measured size is reused: measuring walks every
// file, and a snapshot's private size only grows as the site changes.
const usageTTL = 10 * time.Minute

type usageEntry struct {
	u  fsutil.Usage
	at time.Time
}

// usageCache keeps measured sizes by snapshot folder, for the daemon.
var usageCache = struct {
	sync.Mutex
	m map[string]usageEntry
}{m: map[string]usageEntry{}}

// SnapshotUsage measures the snapshots of a site, or of every site when
// name is "".
func (c *Core) SnapshotUsage(ctx context.Context, name string) (SnapshotsUsage, error) {
	snaps, err := c.allSnapshots(ctx, name)
	if err != nil {
		return SnapshotsUsage{}, err
	}
	out := SnapshotsUsage{Snapshots: make([]SnapshotUsage, len(snaps))}
	protected := c.protectedSnapshots(ctx, snaps)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i, sn := range snaps {
		out.Snapshots[i] = SnapshotUsage{Snapshot: sn, Protected: protected[sn.Site+"/"+sn.ID]}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			u := measureSnapshot(filepath.Dir(sn.FilesDir))
			out.Snapshots[i].Apparent, out.Snapshots[i].Private = u.Apparent, u.Private
		}()
	}
	wg.Wait()
	for _, s := range out.Snapshots {
		out.Apparent += s.Apparent
		out.Private += s.Private
	}
	if err := ctx.Err(); err != nil {
		return out, fmt.Errorf("measure snapshots: %w", err)
	}
	return out, nil
}

func measureSnapshot(dir string) fsutil.Usage {
	usageCache.Lock()
	e, ok := usageCache.m[dir]
	usageCache.Unlock()
	if ok && time.Since(e.at) < usageTTL {
		return e.u
	}
	u, _ := fsutil.DiskUsage(dir)
	usageCache.Lock()
	usageCache.m[dir] = usageEntry{u: u, at: time.Now()}
	usageCache.Unlock()
	return u
}

func forgetUsage(dir string) {
	usageCache.Lock()
	delete(usageCache.m, dir)
	usageCache.Unlock()
}

// allSnapshots lists a site's snapshots, or every site's, newest first.
func (c *Core) allSnapshots(ctx context.Context, name string) ([]store.Snapshot, error) {
	if name != "" {
		return c.ListSnapshots(ctx, name)
	}
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var all []store.Snapshot
	for _, s := range sites {
		snaps, err := c.Store.ListSnapshots(ctx, s.Name)
		if err != nil {
			return nil, err
		}
		all = append(all, snaps...)
	}
	slices.SortFunc(all, func(a, b store.Snapshot) int { return b.CreatedAt.Compare(a.CreatedAt) })
	return all, nil
}

// protectedSnapshots are the snapshots of open agent sessions, by site/id.
func (c *Core) protectedSnapshots(ctx context.Context, snaps []store.Snapshot) map[string]bool {
	open := map[string]string{}
	out := map[string]bool{}
	for _, sn := range snaps {
		id, seen := open[sn.Site]
		if !seen {
			if s, err := c.Store.OpenAgentSession(ctx, sn.Site); err == nil {
				id = s.ID
			}
			open[sn.Site] = id
		}
		if id != "" && sn.SessionID == id {
			out[sn.Site+"/"+sn.ID] = true
		}
	}
	return out
}

// PruneRequest says which snapshots to delete.
type PruneRequest struct {
	// Site limits the clean-up to one site ("" for all).
	Site string
	// OlderThan deletes snapshots taken longer ago than this.
	OlderThan time.Duration
	// DryRun lists them without deleting anything.
	DryRun bool
}

// PruneResult is what a clean-up deleted (or would delete).
type PruneResult struct {
	Removed []SnapshotUsage `json:"removed"`
	// Freed is the space given back (the private size of each).
	Freed int64 `json:"freed"`
	// Kept counts old snapshots spared because an agent session uses them.
	Kept   int  `json:"kept"`
	DryRun bool `json:"dry_run"`
}

// PruneSnapshots deletes the snapshots older than req.OlderThan, of a site
// or of every site, except those of open agent sessions.
func (c *Core) PruneSnapshots(ctx context.Context, req PruneRequest) (PruneResult, error) {
	if req.OlderThan <= 0 {
		return PruneResult{}, errcode.Errorf(errcode.InvalidInput, "say how old: e.g. 30d or 12h")
	}
	res := PruneResult{Removed: []SnapshotUsage{}, DryRun: req.DryRun}
	err := c.locked(ctx, func() error {
		usage, err := c.SnapshotUsage(ctx, req.Site)
		if err != nil {
			return err
		}
		cutoff := time.Now().Add(-req.OlderThan)
		for _, s := range usage.Snapshots {
			if !s.CreatedAt.Before(cutoff) {
				continue
			}
			if s.Protected {
				res.Kept++
				continue
			}
			if !req.DryRun {
				if err := c.removeSnapshot(ctx, s.Snapshot); err != nil {
					return err
				}
			}
			res.Removed = append(res.Removed, s)
			res.Freed += s.Private
		}
		return nil
	})
	return res, err
}
