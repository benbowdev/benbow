// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package hosts

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// mampHosts is shaped like a real /etc/hosts with MAMP PRO entries.
const mampHosts = `##
# Host Database
#
# localhost is used to configure the loopback interface
# when the system is booting.  Do not change this entry.
##
127.0.0.1	localhost
255.255.255.255	broadcasthost
::1             localhost
# 203.0.113.10 agency.example
127.0.0.1	studio.local	# MAMP PRO - Do NOT remove this entry!
::1			studio.local	# MAMP PRO - Do NOT remove this entry!
127.0.0.1	agency.local	# MAMP PRO - Do NOT remove this entry!
::1			agency.local	# MAMP PRO - Do NOT remove this entry!
`

func TestSyncGolden(t *testing.T) {
	got, err := Sync(mampHosts, []string{"shop.local", "demo.benbow.local", "shop.local"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "hosts-with-benbow")
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (run with -update): %v", err)
	}
	if got != string(want) {
		t.Fatalf("mismatch:\n%s", got)
	}
	if !strings.HasPrefix(got, mampHosts) {
		t.Fatal("content before the block changed")
	}
	names, err := Names(got)
	if err != nil || !slices.Equal(names, []string{"demo.benbow.local", "shop.local"}) {
		t.Fatalf("Names = %v, %v", names, err)
	}
}

func TestRoundTripRestoresOriginal(t *testing.T) {
	for name, original := range map[string]string{
		"mamp":  mampHosts,
		"empty": "",
		"crlf":  "127.0.0.1\tlocalhost\r\n",
	} {
		t.Run(name, func(t *testing.T) {
			with, err := Sync(original, []string{"a.local"})
			if err != nil {
				t.Fatal(err)
			}
			changed, err := Sync(with, []string{"a.local", "b.local"})
			if err != nil {
				t.Fatal(err)
			}
			back, err := Sync(changed, nil)
			if err != nil {
				t.Fatal(err)
			}
			if back != original {
				t.Fatalf("not restored:\n%q\nwant\n%q", back, original)
			}
		})
	}
}

func TestBlockInTheMiddleStaysInPlace(t *testing.T) {
	content := "head\n" + Begin + "\n127.0.0.1\told.local\n::1\t\told.local\n" + End + "\ntail\n"
	got, err := Sync(content, []string{"new.local"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "head\n"+Begin) || !strings.HasSuffix(got, End+"\ntail\n") || strings.Contains(got, "old.local") {
		t.Fatalf("unexpected:\n%s", got)
	}
}

func TestMalformedMarkersWriteNothing(t *testing.T) {
	for name, content := range map[string]string{
		"begin only":   mampHosts + Begin + "\n",
		"end only":     mampHosts + End + "\n",
		"end first":    End + "\n" + Begin + "\n",
		"two blocks":   Begin + "\n" + End + "\n" + Begin + "\n" + End + "\n",
		"nested begin": Begin + "\n" + Begin + "\n" + End + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Sync(content, []string{"a.local"}); !errors.Is(err, ErrMarkers) {
				t.Fatalf("err = %v", err)
			}
			if _, err := Sync(content, nil); !errors.Is(err, ErrMarkers) {
				t.Fatalf("clear err = %v", err)
			}
		})
	}
}

func TestRejectsBadNames(t *testing.T) {
	for _, n := range []string{
		"example.com", "local", ".local", "a..local", "A.local", "*.local", "a.local.", "a b.local",
		"-a.local", "a-.local", "a.local\n127.0.0.1 evil.com", "a.localhost", "a.test", "127.0.0.1",
		strings.Repeat("a", 64) + ".local", strings.Repeat("a.", 126) + "local",
	} {
		if _, err := Sync(mampHosts, []string{n}); err == nil {
			t.Errorf("accepted %q", n)
		}
	}
	many := make([]string, MaxNames+1)
	for i := range many {
		many[i] = "a.local"
	}
	if _, err := Sync(mampHosts, many); err == nil {
		t.Error("accepted too many names")
	}
}

func FuzzSync(f *testing.F) {
	f.Add(mampHosts, "a.local")
	f.Add(mampHosts+Begin+"\n"+End+"\n", "b.local")
	f.Add("", "")
	f.Fuzz(func(t *testing.T, content, name string) {
		var names []string
		if name != "" {
			names = []string{name}
		}
		got, err := Sync(content, names)
		if err != nil {
			return
		}
		// Whatever happened, the result can be cleared back, and nothing
		// outside the block was lost.
		back, err := Sync(got, nil)
		if err != nil {
			t.Fatalf("result cannot be cleared: %v", err)
		}
		if _, _, ok, _ := find(content); !ok && strings.HasSuffix(content, "\n") && back != content {
			t.Fatalf("round trip changed content:\n%q\n%q", content, back)
		}
		for line := range strings.Lines(got) {
			if strings.Contains(line, "\t") && !strings.Contains(content, strings.TrimRight(line, "\r\n")) {
				f := strings.Fields(line)
				if len(f) != 2 || (f[0] != "127.0.0.1" && f[0] != "::1") || !ValidName(f[1]) {
					t.Fatalf("Sync wrote an unexpected line %q", line)
				}
			}
		}
	})
}

func TestSyncWithoutFinalNewline(t *testing.T) {
	for _, original := range []string{
		"127.0.0.1\tlocalhost\n86.107.37.100 example.it",
		"127.0.0.1\tlocalhost\n",
		"",
		"127.0.0.1\tlocalhost",
	} {
		one, err := Sync(original, []string{"shop.local"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(one, strings.TrimSuffix(original, "\n")) || !strings.Contains(one, "127.0.0.1\tshop.local") {
			t.Fatalf("add to %q = %q", original, one)
		}
		two, err := Sync(one, []string{"blog.local", "shop.local"})
		if err != nil {
			t.Fatal(err)
		}
		if n, _ := Names(two); len(n) != 2 {
			t.Fatalf("names after update = %v", n)
		}
		back, err := Sync(two, nil)
		if err != nil {
			t.Fatal(err)
		}
		if back != original {
			t.Errorf("clear of %q gave %q", original, back)
		}
	}
}
