// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package proctree follows the processes a command starts, so they can all
// be stopped: a dev server's children (esbuild, workers, a second server)
// and the odd grandchild that leaves its process group with setsid.
package proctree

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// proc identifies a process across pid reuse: a pid plus its start time.
type proc struct {
	pid   int
	start int64 // microseconds since the epoch
}

type info struct {
	ppid, pgid int
	uid        uint32
	start      int64
}

// sZomb is a process that has exited (sys/proc.h).
const sZomb = 5

// table reads every process of the system (pid → parent, group, start).
func table() (map[int]info, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	t := make(map[int]info, len(procs))
	for _, p := range procs {
		if p.Proc.P_stat == sZomb {
			continue // already gone, waiting for its parent to reap it
		}
		st := p.Proc.P_starttime
		t[int(p.Proc.P_pid)] = info{
			ppid:  int(p.Eproc.Ppid),
			pgid:  int(p.Eproc.Pgid),
			uid:   p.Eproc.Ucred.Uid,
			start: st.Sec*1e6 + int64(st.Usec),
		}
	}
	return t, nil
}

// Tracker remembers every process seen under a root process (its
// descendants and its process group), even after their parent exits and
// they are adopted by launchd.
type Tracker struct {
	root int
	// tag is an environment entry ("KEY=value") the root was started
	// with: processes carrying it are the root's even when they were
	// started and orphaned between two scans.
	tag  []byte
	mu   sync.Mutex
	seen map[proc]bool
}

// New tracks root, the leader of its own process group. tag, if not
// empty, is a "KEY=value" entry of root's environment that its
// descendants inherit.
func New(root int, tag string) *Tracker {
	t := &Tracker{root: root, seen: map[proc]bool{}}
	if tag != "" {
		t.tag = []byte(tag + "\x00")
	}
	return t
}

// scanTagged records this user's processes whose environment carries the
// tag. Reading every process's environment costs more than a scan, so it
// only runs when stopping.
func (t *Tracker) scanTagged(tab map[int]info) {
	if t.tag == nil {
		return
	}
	uid := uint32(os.Getuid())
	for pid, i := range tab {
		if i.uid != uid || pid <= 1 || pid == os.Getpid() || t.seen[proc{pid, i.start}] {
			continue
		}
		// argc, the executable, argv, then the environment.
		args, err := unix.SysctlRaw("kern.procargs2", pid)
		if err == nil && bytes.Contains(args, t.tag) {
			t.seen[proc{pid, i.start}] = true
		}
	}
}

// Scan records the processes under the root now.
func (t *Tracker) Scan() {
	tab, err := table()
	if err != nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scan(tab)
}

func (t *Tracker) scan(tab map[int]info) {
	under := map[int]bool{t.root: true}
	for p := range t.seen {
		if i, ok := tab[p.pid]; ok && i.start == p.start {
			under[p.pid] = true
		}
	}
	// Walk down from every known process until nothing new is found.
	for changed := true; changed; {
		changed = false
		for pid, i := range tab {
			if under[pid] || pid <= 1 {
				continue
			}
			if under[i.ppid] || i.pgid == t.root {
				under[pid], changed = true, true
			}
		}
	}
	for pid := range under {
		if i, ok := tab[pid]; ok {
			t.seen[proc{pid, i.start}] = true
		}
	}
}

// Watch scans every interval until stop is closed.
func (t *Tracker) Watch(interval time.Duration, stop <-chan struct{}) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		t.Scan()
		select {
		case <-stop:
			return
		case <-tick.C:
		}
	}
}

// Alive returns the tracked processes still running (pid reuse excluded).
func (t *Tracker) Alive() []int {
	tab, err := table()
	if err != nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.scan(tab)
	var pids []int
	for p := range t.seen {
		if i, ok := tab[p.pid]; ok && i.start == p.start {
			pids = append(pids, p.pid)
		}
	}
	return pids
}

// Signal sends sig to the root's process group and to every tracked
// process still alive.
func (t *Tracker) Signal(sig syscall.Signal) {
	_ = syscall.Kill(-t.root, sig)
	for _, pid := range t.Alive() {
		_ = syscall.Kill(pid, sig)
	}
}

// Stop asks every tracked process to end (SIGTERM), waits up to grace, then
// kills what is left (SIGKILL) and waits up to a second more. It returns the
// pids still alive at the end (normally none).
func (t *Tracker) Stop(grace time.Duration) []int {
	if tab, err := table(); err == nil {
		t.mu.Lock()
		t.scan(tab)
		t.scanTagged(tab)
		t.mu.Unlock()
	}
	t.Signal(syscall.SIGTERM)
	if waitGone(t, grace) {
		return nil
	}
	t.Signal(syscall.SIGKILL)
	waitGone(t, time.Second)
	return t.Alive()
}

func waitGone(t *Tracker, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if len(t.Alive()) == 0 {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Running reports whether pid exists.
func Running(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
