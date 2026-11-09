// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/ports"
	"github.com/benbowdev/benbow/internal/proctree"
	"github.com/benbowdev/benbow/internal/store"
)

// Apps Benbow starts (docs/apps.md) run as a launchd agent per site whose
// program is the hidden `benbow app run <site>`: it starts the user's command
// through their login shell, follows every process it starts and stops them
// all when the agent is unloaded. launchd never restarts it: a dev server
// that crashed stays down, with a clear message, until the user restarts it.

// Logs of the processes Benbow runs: an app's output, a PHP site's
// companion's output (stdout and stderr of the command).
const (
	LogApp       = "app"
	LogCompanion = "companion"
)

const (
	// appStartTimeout is how long an app may take to open its port before
	// its state turns to error.
	appStartTimeout = 90 * time.Second
	// appStopGrace is how long the command's processes get to exit after
	// SIGTERM before they are killed.
	appStopGrace = 5 * time.Second
	// appExitTimeout is launchd's wait after SIGTERM before it kills the
	// wrapper; longer than appStopGrace so the wrapper always finishes.
	appExitTimeout = 15
	appLogTail     = 20
)

// App states (AppStatus.State).
const (
	AppStarting = "starting" // running, the port does not answer yet
	AppRunning  = "running"  // the port answers
	AppExited   = "exited"   // the command ended by itself with status 0
	AppError    = "error"    // the command failed, or never opened its port
	AppStopped  = "stopped"  // not started: Benbow or the site is off
	AppWaiting  = "waiting"  // a proxied app: nothing listens on its port
)

// AppStatus is the state of an app's dev server.
type AppStatus struct {
	State string `json:"state"`
	Port  int    `json:"port"`
	// PID is the wrapper's process (Benbow's `app run`), while it runs.
	PID int `json:"pid,omitempty"`
	// ExitCode is the command's exit status once it has ended.
	ExitCode *int `json:"exit_code,omitempty"`
	// Message explains an error or what to do next.
	Message string    `json:"message,omitempty"`
	Since   time.Time `json:"since,omitzero"`
	Log     string    `json:"log,omitempty"`
	// LogTail is the end of the app's output when it failed or ended.
	LogTail []string `json:"log_tail,omitempty"`
}

// appRunState is what the wrapper records about its run, in
// data/apps/<site>.json.
type appRunState struct {
	PID      int       `json:"pid"`
	Port     int       `json:"port"`
	Started  time.Time `json:"started"`
	Ended    time.Time `json:"ended,omitzero"`
	ExitCode *int      `json:"exit_code,omitempty"`
	// Stopped is set when Benbow stopped the app (not a crash).
	Stopped bool   `json:"stopped,omitempty"`
	Message string `json:"message,omitempty"`
}

// devProc is a dev server Benbow runs for a site: an app's command, or the
// companion of a PHP site (e.g. Vite next to Laravel).
type devProc struct {
	Site      store.Site
	Companion bool
}

func (p devProc) command() string {
	if p.Companion {
		return p.Site.CompanionCommand
	}
	return p.Site.Command
}

func (p devProc) port() int {
	if p.Companion {
		return p.Site.CompanionPort
	}
	return p.Site.AppPort
}

// dir is where the command runs: an app's folder, or a PHP site's project
// root (the Laravel folder, not public/).
func (p devProc) dir() string {
	if p.Companion && p.Site.AppRoot != "" {
		return p.Site.AppRoot
	}
	return p.Site.Path
}

func (p devProc) logKind() string {
	if p.Companion {
		return LogCompanion
	}
	return LogApp
}

// what names the process in messages.
func (p devProc) what() string {
	if p.Companion {
		return "companion"
	}
	return "app"
}

// sitePrcs are the processes Benbow runs for a site now.
func siteProcs(s store.Site) []devProc {
	switch {
	case !s.Enabled:
		return nil
	case s.IsApp() && s.AppMode == store.AppRun:
		return []devProc{{Site: s}}
	case !s.IsApp() && s.CompanionCommand != "":
		return []devProc{{Site: s, Companion: true}}
	}
	return nil
}

func (c *Core) procLabel(p devProc) string {
	if p.Companion {
		return c.launchctl.Prefix + ".companion-" + p.Site.Name
	}
	return c.appLabel(p.Site.Name)
}

func (c *Core) appLabel(site string) string { return c.launchctl.Prefix + ".app-" + site }

func (c *Core) procStatePath(p devProc) string {
	name := p.Site.Name
	if p.Companion {
		name += ".companion"
	}
	return filepath.Join(c.Layout.Data, "apps", name+".json")
}

func (c *Core) readAppState(p devProc) (appRunState, bool) {
	var st appRunState
	b, err := os.ReadFile(c.procStatePath(p))
	if err != nil || json.Unmarshal(b, &st) != nil {
		return appRunState{}, false
	}
	return st, true
}

func (c *Core) writeAppState(p devProc, st appRunState) {
	b, _ := json.Marshal(st)
	if err := os.MkdirAll(filepath.Dir(c.procStatePath(p)), 0o700); err == nil {
		_, _ = fsutil.WriteAtomic(c.procStatePath(p), b, 0o600)
	}
}

// benbowExecutable is the benbow binary apps' agents run (the one running now).
func benbowExecutable() string {
	exe, err := os.Executable()
	if err != nil || filepath.Base(exe) != "benbow" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		return resolved
	}
	return exe
}

// appAgents returns the launchd agents of the apps and companions Benbow
// starts.
func (c *Core) appAgents(ctx context.Context, sites []store.Site) ([]launchd.Agent, error) {
	var agents []launchd.Agent
	services := ""
	for _, s := range sites {
		for _, p := range siteProcs(s) {
			if c.Executable == "" {
				return nil, errcode.Errorf(errcode.Service, "cannot start apps: this program is not the benbow binary")
			}
			if services == "" {
				services = c.appServicesHash(ctx) + ";"
			}
			agents = append(agents, c.appAgent(p, services))
		}
	}
	return agents, nil
}

// appAgent is the agent of one process. services fingerprints the Benbow
// services its environment comes from (appServicesHash).
func (c *Core) appAgent(p devProc, services string) launchd.Agent {
	s := p.Site
	// The wrapper reads the site from the store; the hash makes a changed
	// command, port, folder, database, domain or service change the plist,
	// which restarts the process. It holds no secret.
	sum := sha256.Sum256([]byte(strings.Join([]string{p.dir(), p.command(), strconv.Itoa(p.port()),
		s.Database, s.DBUser, s.DBEngine, s.Domain, strings.Join(s.Aliases, ","), s.Framework, services}, "\x00")))
	env := map[string]string{"BENBOW_CONFIG_HASH": hex.EncodeToString(sum[:8])}
	// The wrapper must find the same Benbow home and keychain items.
	for _, k := range []string{paths.EnvHome, launchd.EnvLabelPrefix} {
		if v := os.Getenv(k); v != "" {
			env[k] = v
		}
	}
	args := []string{"app", "run", s.Name}
	if p.Companion {
		args = append(args, "--companion")
	}
	home, _ := os.UserHomeDir()
	log := c.siteLogPath(s.Name, p.logKind())
	return launchd.Agent{
		Label:       c.procLabel(p),
		Program:     c.Executable,
		Args:        args,
		Env:         env,
		WorkDir:     home,
		StdoutLog:   log,
		StderrLog:   log,
		ExitTimeout: appExitTimeout,
		NoKeepAlive: true,
	}
}

// restartExitedApps unloads the apps and companions whose command has
// ended, so the next reconcile starts them again (Start is the user asking
// for everything to run). Callers hold the lock.
func (c *Core) restartExitedApps(ctx context.Context) error {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return err
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return err
	}
	for _, s := range sites {
		for _, p := range []devProc{{Site: s}, {Site: s, Companion: true}} {
			label := c.procLabel(p)
			if st, ok := loaded[label]; ok && !st.Running {
				if err := c.launchctl.Bootout(ctx, label); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// RestartApp stops an app's command, or a PHP site's companion, with all
// its processes, and starts it again.
func (c *Core) RestartApp(ctx context.Context, name string) (AppStatus, error) {
	var p devProc
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		switch {
		case !site.IsApp() && site.CompanionCommand == "":
			return errcode.Newf(errcode.InvalidInput, "%s is a PHP site without a companion: there is no command to restart", name)
		case site.IsApp() && site.AppMode != store.AppRun:
			return errcode.Newf(errcode.InvalidInput, "%s only proxies to port %d: restart the dev server where you started it", name, site.AppPort)
		case !site.Enabled:
			return errcode.Newf(errcode.InvalidInput, "%s is turned off: turn it on to start it", name)
		}
		p = devProc{Site: site, Companion: !site.IsApp()}
		if on, _, err := c.Store.Setting(ctx, settingRunning); err != nil {
			return err
		} else if on != "1" {
			return errcode.Errorf(errcode.InvalidInput, "Benbow is stopped: start it to run the "+p.what())
		}
		if err := c.launchctl.Bootout(ctx, c.procLabel(p)); err != nil {
			return err
		}
		_, err = c.reconcile(ctx)
		return err
	})
	if err != nil {
		return AppStatus{}, err
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return AppStatus{}, err
	}
	return *c.procStatus(p, loaded), nil
}

// appStatus is the state of an app site's dev server (nil for PHP sites).
func (c *Core) appStatus(s store.Site, loaded map[string]launchd.Status) *AppStatus {
	if !s.IsApp() {
		return nil
	}
	if s.AppMode == store.AppProxy {
		st := &AppStatus{Port: s.AppPort, State: AppRunning}
		if !portAnswers(s.AppPort) {
			st.State = AppWaiting
			st.Message = fmt.Sprintf("Nothing answers on port %d: start your dev server on it.", s.AppPort)
		}
		return st
	}
	return c.procStatus(devProc{Site: s}, loaded)
}

// companionStatus is the state of a PHP site's companion (nil without one).
func (c *Core) companionStatus(s store.Site, loaded map[string]launchd.Status) *AppStatus {
	if s.IsApp() || s.CompanionCommand == "" {
		return nil
	}
	return c.procStatus(devProc{Site: s, Companion: true}, loaded)
}

// procStatus is the state of a process Benbow runs.
func (c *Core) procStatus(p devProc, loaded map[string]launchd.Status) *AppStatus {
	st := &AppStatus{Port: p.port(), Log: c.siteLogPath(p.Site.Name, p.logKind())}
	ls, loadedNow := loaded[c.procLabel(p)]
	if !p.Site.Enabled || !loadedNow {
		st.State = AppStopped
		return st
	}
	run, ok := c.readAppState(p)
	if ls.Running {
		st.PID = ls.PID
		if ok && run.PID == ls.PID {
			st.Since = run.Started
		}
		switch {
		case portAnswers(p.port()):
			st.State = AppRunning
		case st.Since.IsZero() || time.Since(st.Since) < appStartTimeout:
			st.State = AppStarting
		default:
			st.State = AppError
			st.Message = fmt.Sprintf("The command is running but nothing answers on port %d: make the dev server listen on $PORT, or put {port} in the command where it takes its port.", p.port())
			st.LogTail = c.appLogTail(p)
		}
		return st
	}
	code := ls.LastExit
	if ok && run.ExitCode != nil {
		code = *run.ExitCode
		st.Since = run.Ended
	}
	st.ExitCode = &code
	st.LogTail = c.appLogTail(p)
	st.State, st.Message = AppError, diagnoseApp(code, st.LogTail, p.port(), run.Message)
	if code == 0 && run.Message == "" {
		st.State = AppExited
	}
	return st
}

// appLogTail is the process's output since its last start.
func (c *Core) appLogTail(p devProc) []string {
	lines, _, _ := tailLines(c.siteLogPath(p.Site.Name, p.logKind()), 200)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.HasPrefix(lines[i], appStartMarker) && strings.Contains(lines[i], appStartWord) {
			lines = lines[i+1:]
			break
		}
	}
	out := []string{}
	for _, l := range lines {
		if !strings.HasPrefix(l, appStartMarker) {
			out = append(out, l)
		}
	}
	return out[max(len(out)-appLogTail, 0):]
}

var (
	reNotFound = []*regexp.Regexp{
		regexp.MustCompile(`command not found: ([^\s:]+)`),              // zsh
		regexp.MustCompile(`(?:^|: )([^\s:]+): command not found`),      // bash
		regexp.MustCompile(`env: ([^\s:]+): No such file or directory`), // #!/usr/bin/env node
		regexp.MustCompile(`Unknown command: ([^\s:]+)`),                // fish
	}
	reInUse = regexp.MustCompile(`(?i)EADDRINUSE|address already in use|port \d+ is (?:already )?in use`)
)

// versionManagers is what to suggest when a runtime is missing.
var versionManagers = map[string]string{
	"node": "nvm, fnm, mise, volta…", "npm": "nvm, fnm, mise, volta…", "npx": "nvm, fnm, mise, volta…",
	"pnpm": "corepack or its installer", "yarn": "corepack or its installer", "bun": "its installer",
	"python": "pyenv, uv, mise…", "python3": "pyenv, uv, mise…", "uv": "its installer", "poetry": "its installer",
	"ruby": "rbenv, mise…", "bundle": "rbenv, mise…",
}

// diagnoseApp explains why an app's command ended.
func diagnoseApp(code int, tail []string, port int, recorded string) string {
	if recorded != "" {
		return recorded
	}
	text := strings.Join(tail, "\n")
	for _, re := range reNotFound {
		if m := re.FindStringSubmatch(text); m != nil {
			prog := m[1]
			msg := prog + " was not found by your login shell."
			if how, ok := versionManagers[prog]; ok {
				msg += " Install it with your version manager (" + how + ")"
			} else {
				msg += " Install it"
			}
			return msg + " and check that your shell profile (.zprofile, .zshrc) puts it on PATH. Benbow runs the command as a terminal would and does not install runtimes."
		}
	}
	if reInUse.MatchString(text) {
		return fmt.Sprintf("The port is already in use. Benbow gave the command port %d: if the dev server picks its own port, make it use $PORT or put {port} in the command.", port)
	}
	switch {
	case code == 0:
		return "The command finished. A dev server keeps running: check the command."
	case code == 127:
		return "A program in the command was not found by your login shell."
	case code > 128:
		return fmt.Sprintf("The command was killed by signal %d.", code-128)
	}
	return fmt.Sprintf("The command exited with status %d: see the app log.", code)
}

// portAnswers reports whether something accepts connections on port, over
// IPv4 or IPv6 loopback.
func portAnswers(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return true
		}
	}
	return false
}

// appStartMarker starts the lines the wrapper writes in the app log; the
// one before each run has appStartWord.
const (
	appStartMarker = "[benbow] "
	appStartWord   = " starting "
)

// loginShell is the user's login shell (launchd does not set SHELL).
func loginShell(ctx context.Context) string {
	if u, err := user.Current(); err == nil {
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/bin/dscl", ".", "-read", "/Users/"+u.Username, "UserShell").Output()
		if err == nil {
			if _, sh, ok := strings.Cut(strings.TrimSpace(string(out)), "UserShell:"); ok && isExecutable(strings.TrimSpace(sh)) {
				return strings.TrimSpace(sh)
			}
		}
	}
	if sh := os.Getenv("SHELL"); isExecutable(sh) {
		return sh
	}
	return "/bin/zsh"
}

func isExecutable(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

// appCommand builds the login shell invocation for a site's command: the
// shell reads the user's profile (nvm, pyenv, PATH…), changes to the
// project folder (which fires the directory hooks of fnm, mise, direnv…)
// and runs the command with {port} replaced.
func appCommand(shell string, p devProc) []string {
	cmd := strings.ReplaceAll(p.command(), "{port}", strconv.Itoa(p.port()))
	script := `cd "$BENBOW_APP_DIR" || exit 1` + "\n" + cmd
	return []string{shell, "-l", "-i", "-c", script}
}

// appEnv is the environment of the command: the wrapper's own, without
// Benbow's variables, plus the port and Benbow's CA for HTTPS calls to other
// Benbow sites.
func (c *Core) appEnv(p devProc, shell string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		switch {
		case strings.HasPrefix(k, "BENBOW_"), k == "TERM", k == "SHELL", k == "PORT":
			continue
		}
		env = append(env, kv)
	}
	home, _ := os.UserHomeDir()
	env = append(env,
		"HOME="+home,
		"SHELL="+shell,
		"TERM=dumb",
		"NO_COLOR=1",
		"BROWSER=none",       // dev servers that open a browser tab on start
		"PYTHONUNBUFFERED=1", // Python's output reaches the log at once
		"PORT="+strconv.Itoa(p.port()),
		"BENBOW_APP_DIR="+p.dir(),
		"NODE_EXTRA_CA_CERTS="+c.CACertPath(),
	)
	if _, err := os.Stat(c.caBundlePath()); err == nil {
		env = append(env, "SSL_CERT_FILE="+c.caBundlePath(), "REQUESTS_CA_BUNDLE="+c.caBundlePath())
	}
	return env
}

// RunApp is the program of an app's (or a companion's) launchd agent: it
// runs the command until it exits or the agent is unloaded, and returns
// the exit status for the agent. Its output is the app (companion) log.
func (c *Core) RunApp(ctx context.Context, name string, companion bool) int {
	say := func(format string, a ...any) {
		fmt.Printf(appStartMarker+time.Now().Format("2006-01-02 15:04:05")+" "+format+"\n", a...)
	}
	s, err := c.Store.Site(ctx, name)
	if err != nil {
		say("%v", err)
		return 1
	}
	p := devProc{Site: s, Companion: companion}
	run := appRunState{PID: os.Getpid(), Port: p.port(), Started: time.Now()}
	fail := func(msg string) int {
		say("%s", msg)
		code := 1
		run.Ended, run.ExitCode, run.Message = time.Now(), &code, msg
		c.writeAppState(p, run)
		return code
	}
	switch {
	case companion && (s.IsApp() || s.CompanionCommand == ""):
		return fail(name + " has no companion command.")
	case !companion && (!s.IsApp() || s.AppMode != store.AppRun):
		return fail(name + " is not an app Benbow starts.")
	}
	shell := loginShell(ctx)
	say(strings.TrimSpace(appStartWord)+" %q in %s with %s, port %d", p.command(), p.dir(), shell, p.port())
	if fi, err := os.Stat(p.dir()); err != nil || !fi.IsDir() {
		return fail(fmt.Sprintf("The folder %s is missing.", p.dir()))
	}
	if !ports.Free(p.port()) {
		flag := "--port"
		if companion {
			flag = "--companion-port"
		}
		return fail(fmt.Sprintf("Port %d is in use by %s: stop it, or give the %s another port (benbow site set %s %s N).",
			p.port(), ports.Describe(ports.Owners(ctx, p.port())), p.what(), name, flag))
	}
	var extra, names, warnings []string
	if companion {
		extra, names, warnings = c.companionEnvironment(ctx, s)
	} else {
		extra, names, warnings = c.appEnvironment(ctx, s)
	}
	if line := describeEnv(names); line != "" {
		say("%s", line)
	}
	for _, w := range warnings {
		say("%s", w)
	}
	argv := appCommand(shell, p)
	cmd := exec.Command(argv[0], argv[1:]...)
	home, _ := os.UserHomeDir()
	// Every process the command starts inherits the tag, which finds the
	// ones that leave the process tree too fast to be seen in it.
	tag := "BENBOW_APP_RUN=" + name + "-" + p.what() + "-" + strconv.FormatInt(run.Started.UnixNano(), 36)
	cmd.Dir, cmd.Env = home, append(append(c.appEnv(p, shell), extra...), tag)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer signal.Stop(sigs)
	if err := cmd.Start(); err != nil {
		return fail(fmt.Sprintf("Could not start %s: %v", shell, err))
	}
	c.writeAppState(p, run)
	tree := proctree.New(cmd.Process.Pid, tag)
	stopWatch := make(chan struct{})
	go tree.Watch(2*time.Second, stopWatch)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	var waitErr error
	select {
	case sig := <-sigs:
		say("stopping (%v)", sig)
		close(stopWatch)
		if left := tree.Stop(appStopGrace); len(left) > 0 {
			say("processes still running after SIGKILL: %v", left)
		}
		if err := <-done; err != nil {
			say("the command ended with %v", err)
		}
		code := 0
		run.Ended, run.ExitCode, run.Stopped = time.Now(), &code, true
		c.writeAppState(p, run)
		return 0
	case waitErr = <-done:
		close(stopWatch)
	}
	// The command ended by itself: whatever it left behind goes too.
	tree.Stop(appStopGrace)
	code := exitCode(waitErr)
	run.Ended, run.ExitCode = time.Now(), &code
	c.writeAppState(p, run)
	say("the command exited with status %d", code)
	return code
}

// exitCode turns a Wait error into a shell-style status (128+n for signal n).
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return 1
}

// removeAppState forgets a removed site's last runs.
func (c *Core) removeAppState(s store.Site) {
	for _, p := range []devProc{{Site: s}, {Site: s, Companion: true}} {
		_ = os.Remove(c.procStatePath(p))
	}
	cert, key := c.companionCertPaths(s.Name)
	_ = os.Remove(cert)
	_ = os.Remove(key)
}
