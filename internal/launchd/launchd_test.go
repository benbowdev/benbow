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
	const home = "/Users/test/Library/Application Support/covo"
	got, err := Plist(Agent{
		Label:     "dev.getcovo.caddy",
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
}

func TestParseList(t *testing.T) {
	l := &Launchctl{Prefix: "dev.getcovo"}
	out := "PID\tStatus\tLabel\n" +
		"954\t0\tapplication.de.appsolute.mamppro.1\n" +
		"123\t0\tdev.getcovo.caddy\n" +
		"-\t78\tdev.getcovo.php84\n" +
		"-\t0\tdev.getcovoother.x\n" +
		"-\t0\tdev.getcovo.test-ab.caddy\n"
	got := l.parseList(out)
	want := map[string]Status{
		"dev.getcovo.caddy":         {Label: "dev.getcovo.caddy", PID: 123, Running: true},
		"dev.getcovo.php84":         {Label: "dev.getcovo.php84", LastExit: 78},
		"dev.getcovo.test-ab.caddy": {Label: "dev.getcovo.test-ab.caddy"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseList = %+v\nwant %+v", got, want)
	}
}

// fakeLaunchctl simulates launchd for Apply/StopAll.
type fakeLaunchctl struct {
	loaded map[string]bool
	calls  []string
}

func (f *fakeLaunchctl) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "list":
		var b strings.Builder
		b.WriteString("PID\tStatus\tLabel\n1\t0\tcom.other.service\n")
		for label := range f.loaded {
			b.WriteString("42\t0\t" + label + "\n")
		}
		return b.String(), nil
	case "bootstrap":
		label := strings.TrimSuffix(filepath.Base(args[2]), ".plist")
		f.loaded[label] = true
	case "bootout":
		f.loaded[args[1][strings.LastIndex(args[1], "/")+1:]] = false
		delete(f.loaded, args[1][strings.LastIndex(args[1], "/")+1:])
	}
	return "", nil
}

func TestApplyIsIdempotentAndScoped(t *testing.T) {
	dir := t.TempDir()
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.getcovo.php81": true}}
	l := &Launchctl{Prefix: "dev.getcovo", Run: f.run}
	agents := []Agent{
		{Label: "dev.getcovo.caddy", Program: "/c"},
		{Label: "dev.getcovo.php84", Program: "/p"},
	}

	changes, err := l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	want := []Change{{"dev.getcovo.caddy", "started"}, {"dev.getcovo.php84", "started"}, {"dev.getcovo.php81", "stopped"}}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("first Apply = %v, want %v", changes, want)
	}

	changes, err = l.Apply(context.Background(), dir, agents)
	if err != nil {
		t.Fatal(err)
	}
	want = []Change{{"dev.getcovo.caddy", "unchanged"}, {"dev.getcovo.php84", "unchanged"}}
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

func TestRefusesForeignLabels(t *testing.T) {
	f := &fakeLaunchctl{loaded: map[string]bool{}}
	l := &Launchctl{Prefix: "dev.getcovo", Run: f.run}
	if _, err := l.Apply(context.Background(), t.TempDir(), []Agent{{Label: "com.other.service"}}); err == nil {
		t.Fatal("Apply accepted a foreign label")
	}
	if err := l.Bootout(context.Background(), "com.other.service"); err == nil {
		t.Fatal("Bootout accepted a foreign label")
	}
}

func TestStopAll(t *testing.T) {
	f := &fakeLaunchctl{loaded: map[string]bool{"dev.getcovo.caddy": true, "dev.getcovo.php84": true}}
	l := &Launchctl{Prefix: "dev.getcovo", Run: f.run}
	for range 2 { // idempotent
		if _, err := l.StopAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.loaded) != 0 {
		t.Fatalf("still loaded: %v", f.loaded)
	}
}
