// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package proctree

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The test binary plays the processes of a dev server: COVO_PROCTREE_ROLE
// picks the role.
func TestMain(m *testing.M) {
	switch os.Getenv("COVO_PROCTREE_ROLE") {
	case "parent":
		parent()
	case "child":
		// A child that ignores SIGTERM, like a stuck worker.
		signal.Ignore(syscall.SIGTERM)
		time.Sleep(time.Hour)
	case "escaper":
		time.Sleep(time.Hour)
	default:
		os.Exit(m.Run())
	}
}

// parent starts a child in its group and a grandchild in a new session,
// prints their pids, then waits (or exits at once with "exit" as argument).
func parent() {
	start := func(role string, setsid bool) int {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "COVO_PROCTREE_ROLE="+role)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: setsid}
		if err := cmd.Start(); err != nil {
			fmt.Println("error", err)
			os.Exit(1)
		}
		return cmd.Process.Pid
	}
	fmt.Println(start("child", false), start("escaper", true))
	if os.Getenv("COVO_PROCTREE_EXIT") != "" {
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n') // until told to go
		os.Exit(3)
	}
	time.Sleep(time.Hour)
}

func startTree(t *testing.T, exitEarly bool) (*exec.Cmd, io.WriteCloser, []int) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "COVO_PROCTREE_ROLE=parent", testTag)
	if exitEarly {
		cmd.Env = append(cmd.Env, "COVO_PROCTREE_EXIT=1")
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	pids := []int{cmd.Process.Pid}
	for _, f := range strings.Fields(line) {
		pid, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("parent said %q", line)
		}
		pids = append(pids, pid)
	}
	t.Cleanup(func() {
		for _, p := range pids {
			_ = syscall.Kill(p, syscall.SIGKILL)
		}
	})
	return cmd, in, pids
}

// testTag marks this test's processes in their environment.
var testTag = "COVO_PROCTREE_TAG=" + strconv.Itoa(os.Getpid())

func TestStopKillsTheWholeTree(t *testing.T) {
	cmd, _, pids := startTree(t, false)
	go func() { _ = cmd.Wait() }()
	tr := New(cmd.Process.Pid, "")
	tr.Scan()
	if got := len(tr.Alive()); got != 3 {
		t.Fatalf("tracking %d processes, want 3 (%v)", got, pids)
	}
	if left := tr.Stop(500 * time.Millisecond); len(left) != 0 {
		t.Fatalf("still alive after Stop: %v", left)
	}
	for _, p := range pids {
		if Running(p) {
			t.Errorf("pid %d still running", p)
		}
	}
}

func TestStopFindsAnOrphanedGrandchild(t *testing.T) {
	cmd, in, pids := startTree(t, true)
	tr := New(cmd.Process.Pid, "")
	tr.Scan() // seen while the parent was alive
	_ = in.Close()
	_ = cmd.Wait()
	// The escaper is now launchd's child, in its own session.
	if left := tr.Stop(500 * time.Millisecond); len(left) != 0 {
		t.Fatalf("still alive after Stop: %v", left)
	}
	if Running(pids[2]) {
		t.Errorf("escaped grandchild %d still running", pids[2])
	}
}

func TestStopFindsATaggedOrphanNeverScanned(t *testing.T) {
	cmd, in, pids := startTree(t, true)
	tr := New(cmd.Process.Pid, testTag)
	_ = in.Close()
	_ = cmd.Wait() // the parent is gone before any scan
	if left := tr.Stop(500 * time.Millisecond); len(left) != 0 {
		t.Fatalf("still alive after Stop: %v", left)
	}
	if Running(pids[2]) {
		t.Errorf("escaped grandchild %d still running", pids[2])
	}
}
