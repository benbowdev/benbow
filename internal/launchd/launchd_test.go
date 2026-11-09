// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package launchd

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestPlistGolden(t *testing.T) {
	const home = "/Users/test/Library/Application Support/benbow"
	got, err := Plist(Agent{
		Label:     "dev.benbow.caddy",
		Program:   home + "/bin/caddy/2.11.7/caddy",
		Args:      []string{"run", "--config", home + "/etc/caddy/Caddyfile", "--adapter", "caddyfile"},
		Env:       map[string]string{"XDG_DATA_HOME": home + "/data", "A_<&>": "x&y"},
		StdoutLog: home + "/logs/caddy.out.log",
		StderrLog: home + "/logs/caddy.err.log",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "caddy.plist")
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("plist mismatch:\n%s", got)
	}
	if !strings.Contains(string(got), "A_&lt;&amp;&gt;") {
		t.Error("keys must be XML-escaped")
	}
	if strings.Contains(string(got), "ExitTimeOut") {
		t.Error("ExitTimeOut without ExitTimeout")
	}
	if !strings.Contains(string(got), "<key>KeepAlive</key>\n\t<true/>") {
		t.Error("KeepAlive must default to always")
	}
	once, err := Plist(Agent{Label: "x", Program: "/x", StdoutLog: "/o", StderrLog: "/e", NoKeepAlive: true})
	if err != nil || !strings.Contains(string(once), "<key>KeepAlive</key>\n\t<false/>") {
		t.Errorf("NoKeepAlive plist: %v\n%s", err, once)
	}
	onFailure, err := Plist(Agent{Label: "x", Program: "/x", StdoutLog: "/o", StderrLog: "/e", RestartOnFailure: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(onFailure), "<key>KeepAlive</key>\n\t<dict>\n\t\t<key>SuccessfulExit</key>\n\t\t<false/>\n\t</dict>") {
		t.Errorf("RestartOnFailure plist:\n%s", onFailure)
	}
	got, err = Plist(Agent{Label: "x", Program: "/x", StdoutLog: "/o", StderrLog: "/e", ExitTimeout: 60})
	if err != nil || !strings.Contains(string(got), "<key>ExitTimeOut</key>\n\t<integer>60</integer>") {
		t.Errorf("ExitTimeOut missing: %v\n%s", err, got)
	}
}

func TestParseList(t *testing.T) {
	l := &Launchctl{Prefix: "dev.benbow"}
	out := "PID\tStatus\tLabel\n" +
		"954\t0\tapplication.de.appsolute.mamppro.1\n" +
		"123\t0\tdev.benbow.caddy\n" +
		"-\t78\tdev.benbow.php84\n" +
		"-\t0\tdev.benbowother.x\n" +
		"-\t0\tdev.benbow.test-ab.caddy\n"
	got := l.parseList(out)
	want := map[string]Status{
		"dev.benbow.caddy": {Label: "dev.benbow.caddy", PID: 123, Running: true},
		"dev.benbow.php84": {Label: "dev.benbow.php84", LastExit: 78},
		// Another instance's (a test's) labels are not this one's.
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseList = %+v\nwant %+v", got, want)
	}
}

// fakeLaunchctl simulates launchd for Apply/StopAll.
type fakeLaunchctl struct {
	loaded map[string]bool
	// exited are loaded agents whose program has ended.
	exited map[string]bool
	calls  []string
}

func (f *fakeLaunchctl) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "list":
		var b strings.Builder
		b.WriteString("PID\tStatus\tLabel\n1\t0\tcom.other.service\n")
		for label := range f.loaded {
			if f.exited[label] {
				b.WriteString("-\t1\t" + label + "\n")
				continue
			}
			b.WriteString("42\t0\t" + label + "\n")
		}
		return b.String(), nil
	case "bootstrap":
		label := strings.TrimSuffix(filepath.Base(args[2]), ".plist")
		f.loaded[label] = true
		delete(f.exited, label)
	case "bootout":
		f.loaded[args[1][strings.LastIndex(args[1], "/")+1:]] = false
		delete(f.loaded, args[1][strings.LastIndex(args[1], "/")+1:])
	}
	return "", nil
}

func TestApplyIsIdempotentAndScoped(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.benbow.php81": true}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run}
	agents := []Agent{
		{Label: "dev.benbow.caddy", Program: "/c"},
		{Label: "dev.benbow.php84", Program: "/p"},
	}

	changes, err := l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"dev.benbow.caddy", "started"}, {"dev.benbow.php84", "started"}, {"dev.benbow.php81", "stopped"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("first Apply = %v, want %v", changes, want)
	}

	changes, err = l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	want = []Change{{"dev.benbow.caddy", "unchanged"}, {"dev.benbow.php84", "unchanged"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("second Apply = %v, want %v", changes, want)
	}

	agents[0].Args = []string{"--new"}
	changes, err = l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	if changes[0].Action != "restarted" {
		t.Fatalf("changed plist: %v", changes)
	}

	for _, c := range f.calls {
		if strings.Contains(c, "com.other.service") {
			t.Fatalf("touched a foreign service: %q", c)
		}
	}
}

func TestOwnsOnlyItsOwnLevel(t *testing.T) {
	l := &Launchctl{Prefix: "dev.benbow"}
	for label, want := range map[string]bool{
		"dev.benbow.caddy": true, "dev.benbow.app-shop": true, "dev.benbow.php84": true,
		"dev.benbow.test-1a2b.caddy": false, "dev.benbowx.caddy": false, "dev.benbow.": false, "com.other": false,
	} {
		if got := l.Owns(label); got != want {
			t.Errorf("Owns(%q) = %v, want %v", label, got, want)
		}
	}
	test := &Launchctl{Prefix: "dev.benbow.test-1a2b"}
	if !test.Owns("dev.benbow.test-1a2b.caddy") {
		t.Error("a test prefix does not own its own labels")
	}
}

func TestRefusesForeignLabels(t *testing.T) {
	f := &fakeLaunchctl{loaded: map[string]bool{}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run}
	if _, err := l.Apply(context.Background(), t.TempDir(), []Agent{{Label: "com.other.service"}}); err == nil {
		t.Fatal("Apply accepted a foreign label")
	}
	if err := l.Bootout(context.Background(), "com.other.service"); err == nil {
		t.Fatal("Bootout accepted a foreign label")
	}
}

func TestStopAll(t *testing.T) {
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.benbow.caddy": true, "dev.benbow.php84": true}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run}
	for range 2 { // idempotent
		if _, err := l.StopAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.loaded) != 0 {
		t.Fatalf("still loaded: %v", f.loaded)
	}
}

func TestKeepSurvivesApplyAndStopAll(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.benbow.core": true, "dev.benbow.php81": true}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run, Keep: []string{"dev.benbow.core"}}
	if _, err := l.Apply(context.Background(), dir, []Agent{{Label: "dev.benbow.caddy", Program: "/c"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.StopAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := map[string]bool{"dev.benbow.core": true}; !reflect.DeepEqual(f.loaded, want) {
		t.Fatalf("loaded = %v, want %v", f.loaded, want)
	}
}

func TestEnsure(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.benbow.php81": true}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run}
	a := Agent{Label: "dev.benbow.core", Program: "/benbow", Args: []string{"daemon", "run"}}
	for i, want := range []string{"started", "unchanged"} {
		ch, err := l.Ensure(context.Background(), dir, a)
		if err != nil || ch.Action != want {
			t.Fatalf("Ensure #%d = %v %v, want %s", i, ch, err, want)
		}
	}
	a.Env = map[string]string{"BENBOW_BINARY_HASH": "x"}
	if ch, err := l.Ensure(context.Background(), dir, a); err != nil || ch.Action != "restarted" {
		t.Fatalf("changed plist: %v %v", ch, err)
	}
	if !f.loaded["dev.benbow.php81"] {
		t.Fatal("Ensure stopped another agent")
	}
}

func TestApplyLeavesAnExitedNoKeepAliveAgentAlone(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLaunchctl{loaded: map[string]bool{}, exited: map[string]bool{}}
	l := &Launchctl{Prefix: "dev.benbow", Run: f.run}
	agents := []Agent{
		{Label: "dev.benbow.caddy", Program: "/c"},
		{Label: "dev.benbow.app-shop", Program: "/benbow", NoKeepAlive: true},
	}
	if _, err := l.Apply(context.Background(), dir, agents); err != nil {
		t.Fatal(err)
	}
	f.exited["dev.benbow.caddy"], f.exited["dev.benbow.app-shop"] = true, true
	changes, err := l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"dev.benbow.caddy", "restarted"}, {"dev.benbow.app-shop", "unchanged"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("Apply = %v, want %v", changes, want)
	}
	// A changed plist starts it again.
	agents[1].Args = []string{"app", "run", "shop"}
	if changes, err = l.Apply(context.Background(), dir, agents); err != nil || changes[1].Action != "restarted" {
		t.Fatalf("changed plist: %v %v", changes, err)
	}
}
