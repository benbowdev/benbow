// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/store"
)

func TestAppCommand(t *testing.T) {
	s := store.Site{Path: "/Users/me/Code/my shop", Command: "npm run dev -- --port {port} --strictPort", AppPort: 41002}
	got := appCommand("/bin/zsh", devProc{Site: s})
	want := []string{"/bin/zsh", "-l", "-i", "-c", "cd \"$BENBOW_APP_DIR\" || exit 1\nnpm run dev -- --port 41002 --strictPort"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("appCommand = %q, want %q", got, want)
	}

	c := runCore(t)
	t.Setenv("BENBOW_HOME", "/somewhere")
	t.Setenv("PORT", "1")
	env := strings.Join(c.appEnv(devProc{Site: s}, "/bin/zsh"), "\n")
	for _, kv := range []string{"PORT=41002", "BENBOW_APP_DIR=" + s.Path, "SHELL=/bin/zsh", "NO_COLOR=1", "BROWSER=none", "NODE_EXTRA_CA_CERTS=" + c.CACertPath()} {
		if !strings.Contains(env, kv+"\n") && !strings.HasSuffix(env, kv) {
			t.Errorf("env lacks %s", kv)
		}
	}
	if strings.Contains(env, "BENBOW_HOME") || strings.Contains(env, "PORT=1\n") {
		t.Errorf("env leaks Benbow's variables or the old PORT:\n%s", env)
	}
}

func TestDiagnoseApp(t *testing.T) {
	s := store.Site{AppPort: 41000}
	tests := []struct {
		code int
		tail []string
		want string
	}{
		{127, []string{"zsh:2: command not found: node"}, "node was not found by your login shell. Install it with your version manager (nvm"},
		{127, []string{"bash: line 2: pnpm: command not found"}, "pnpm was not found"},
		{127, []string{"env: node: No such file or directory"}, "node was not found"},
		{1, []string{"Error: listen EADDRINUSE: address already in use :::41000"}, "already in use. Benbow gave the command port 41000"},
		{1, []string{"error when starting dev server:", "Error: Port 41000 is already in use"}, "already in use"},
		{127, nil, "A program in the command was not found"},
		{0, nil, "The command finished"},
		{137, nil, "killed by signal 9"},
		{2, []string{"boom"}, "exited with status 2"},
	}
	for _, tt := range tests {
		if got := diagnoseApp(tt.code, tt.tail, s.AppPort, ""); !strings.Contains(got, tt.want) {
			t.Errorf("diagnoseApp(%d, %q) = %q, want it to contain %q", tt.code, tt.tail, got, tt.want)
		}
	}
	if got := diagnoseApp(1, []string{"x"}, s.AppPort, "Port 41000 is in use by node"); got != "Port 41000 is in use by node" {
		t.Errorf("recorded message lost: %q", got)
	}
}

// runCore is appCore with a full layout (logs, data) in a temporary home.
func runCore(t *testing.T) *Core {
	t.Helper()
	c := appCore(t)
	l, err := paths.New(filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	c.Layout = l
	c.launchctl = &launchd.Launchctl{Prefix: "dev.benbow"}
	return c
}

func TestAppStatus(t *testing.T) {
	c := runCore(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	open := l.Addr().(*net.TCPAddr).Port
	closed := freeTestPort(t)

	run := store.Site{Name: "shop", Kind: store.KindApp, AppMode: store.AppRun, AppPort: open, Enabled: true, Command: "npm run dev"}
	label := c.appLabel("shop")
	if label != "dev.benbow.app-shop" {
		t.Errorf("label = %q", label)
	}
	none := map[string]launchd.Status{}
	if st := c.appStatus(store.Site{Name: "php"}, none); st != nil {
		t.Errorf("PHP site has app status %+v", st)
	}
	if st := c.appStatus(run, none); st.State != AppStopped {
		t.Errorf("not loaded: %+v", st)
	}
	if st := c.appStatus(run, map[string]launchd.Status{label: {Running: true, PID: 7}}); st.State != AppRunning || st.PID != 7 {
		t.Errorf("running: %+v", st)
	}
	starting := run
	starting.AppPort = closed
	c.writeAppState(devProc{Site: run}, appRunState{PID: 7, Started: time.Now()})
	if st := c.appStatus(starting, map[string]launchd.Status{label: {Running: true, PID: 7}}); st.State != AppStarting {
		t.Errorf("starting: %+v", st)
	}
	c.writeAppState(devProc{Site: run}, appRunState{PID: 7, Started: time.Now().Add(-2 * appStartTimeout)})
	if st := c.appStatus(starting, map[string]launchd.Status{label: {Running: true, PID: 7}}); st.State != AppError || !strings.Contains(st.Message, "nothing answers") {
		t.Errorf("never opened its port: %+v", st)
	}

	// Exited: the output since the last start explains why.
	if err := os.MkdirAll(c.siteLogDir("shop"), 0o700); err != nil {
		t.Fatal(err)
	}
	log := appStartMarker + "1" + appStartWord + "old run\nzsh:1: command not found: yarn\n" + appStartMarker + "2" + appStartWord + "new run\nzsh:2: command not found: node\n" + appStartMarker + "the command exited with status 127\n"
	if err := os.WriteFile(c.siteLogPath("shop", LogApp), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	code := 127
	c.writeAppState(devProc{Site: run}, appRunState{PID: 7, Started: time.Now(), Ended: time.Now(), ExitCode: &code})
	st := c.appStatus(run, map[string]launchd.Status{label: {LastExit: 127}})
	if st.State != AppError || *st.ExitCode != 127 || !strings.HasPrefix(st.Message, "node was not found") || len(st.LogTail) != 1 {
		t.Errorf("exited: %+v", st)
	}
	zero := 0
	c.writeAppState(devProc{Site: run}, appRunState{ExitCode: &zero})
	if st := c.appStatus(run, map[string]launchd.Status{label: {}}); st.State != AppExited {
		t.Errorf("exit 0: %+v", st)
	}

	proxy := store.Site{Name: "api", Kind: store.KindApp, AppMode: store.AppProxy, AppPort: closed, Enabled: true}
	if st := c.appStatus(proxy, none); st.State != AppWaiting || st.Message == "" {
		t.Errorf("proxied, nothing listening: %+v", st)
	}
	proxy.AppPort = open
	if st := c.appStatus(proxy, none); st.State != AppRunning {
		t.Errorf("proxied, listening: %+v", st)
	}
}

func TestAppAgent(t *testing.T) {
	c := runCore(t)
	if _, err := c.appAgents(t.Context(), []store.Site{{Name: "x", Kind: store.KindApp, AppMode: store.AppRun, Enabled: true}}); err == nil {
		t.Error("app agents without the benbow binary")
	}
	c.Executable = "/Applications/Benbow.app/Contents/MacOS/benbow"
	sites := []store.Site{
		{Name: "shop", Kind: store.KindApp, AppMode: store.AppRun, Enabled: true, Path: "/p", Command: "npm run dev", AppPort: 41000},
		{Name: "off", Kind: store.KindApp, AppMode: store.AppRun, Enabled: false},
		{Name: "api", Kind: store.KindApp, AppMode: store.AppProxy, Enabled: true},
		{Name: "blog", PHPMinor: "8.4", Enabled: true},
	}
	agents, err := c.appAgents(t.Context(), sites)
	if err != nil || len(agents) != 1 {
		t.Fatalf("agents = %+v, %v", agents, err)
	}
	a := agents[0]
	if a.Label != "dev.benbow.app-shop" || !a.NoKeepAlive || a.Program != c.Executable ||
		strings.Join(a.Args, " ") != "app run shop" || a.StdoutLog != filepath.Join(c.siteLogDir("shop"), "app.log") || a.StdoutLog != a.StderrLog {
		t.Errorf("agent = %+v", a)
	}
	other := sites[0]
	other.Command = "pnpm dev"
	if c.appAgent(devProc{Site: other}, "").Env["BENBOW_CONFIG_HASH"] == a.Env["BENBOW_CONFIG_HASH"] {
		t.Error("a new command does not change the plist")
	}
}

func freeTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return p
}
