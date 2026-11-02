// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"encoding/json"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

type statusData struct {
	Running   bool     `json:"running"`
	HTTPSPort int      `json:"https_port"`
	Home      string   `json:"home"`
	Orphans   []string `json:"orphans"`
	Services  []struct {
		Name    string `json:"name"`
		Label   string `json:"label"`
		Running bool   `json:"running"`
		PID     int    `json:"pid"`
	} `json:"services"`
	CA struct {
		CertPath string `json:"cert_path"`
	} `json:"ca"`
	Sites []struct {
		Name string `json:"name"`
		URL  string `json:"url"`
	} `json:"sites"`
}

func (h *harness) status() statusData {
	h.t.Helper()
	var st statusData
	if err := json.Unmarshal(h.mustCovo("status"), &st); err != nil {
		h.t.Fatal(err)
	}
	return st
}

func (h *harness) loadedLabels() []string {
	h.t.Helper()
	out, err := exec.Command("/bin/launchctl", "list").Output()
	if err != nil {
		h.t.Fatal(err)
	}
	var labels []string
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Split(line, "\t")
		if len(f) == 3 && strings.HasPrefix(f[2], h.prefix()+".") {
			labels = append(labels, f[2])
		}
	}
	return labels
}

func (h *harness) prefix() string {
	for _, kv := range h.env {
		if v, ok := strings.CutPrefix(kv, "COVO_LABEL_PREFIX="); ok {
			return v
		}
	}
	h.t.Fatal("no label prefix")
	return ""
}

func TestStartStopIdempotent(t *testing.T) {
	h := newHarness(t)

	for i := range 2 {
		h.mustCovo("start")
		st := h.status()
		if !st.Running || len(st.Services) != 1 || st.Services[0].Name != "caddy" || st.Services[0].PID == 0 {
			t.Fatalf("start #%d: status = %+v", i+1, st)
		}
		if st.HTTPSPort != h.port {
			t.Fatalf("https_port = %d, want %d", st.HTTPSPort, h.port)
		}
	}
	firstPID := h.status().Services[0].PID
	h.mustCovo("start")
	if pid := h.status().Services[0].PID; pid != firstPID {
		t.Fatalf("idempotent start restarted caddy: pid %d -> %d", firstPID, pid)
	}

	for range 2 {
		h.mustCovo("stop")
		if labels := h.loadedLabels(); len(labels) != 0 {
			t.Fatalf("agents still loaded after stop: %v", labels)
		}
	}
	if h.status().Running {
		t.Fatal("status reports running after stop")
	}
}

func TestStartRefusesBusyPort(t *testing.T) {
	h := newHarness(t)
	h.mustCovo("start")
	h.mustCovo("stop")

	// Someone else takes the stored port.
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(h.port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	env := h.covo("start")
	if env.OK || env.Error == nil || env.Error.Code != "port_in_use" {
		t.Fatalf("start on busy port: %+v", env)
	}
	if !strings.Contains(env.Error.Message, "--https-port") {
		t.Errorf("error should suggest an alternative: %q", env.Error.Message)
	}
	if labels := h.loadedLabels(); len(labels) != 0 {
		t.Fatalf("agents loaded despite the port error: %v", labels)
	}

	// The suggested way out works.
	alt := freePort(t)
	h.mustCovo("start", "--https-port", strconv.Itoa(alt))
	if st := h.status(); st.HTTPSPort != alt || !st.Running {
		t.Fatalf("after --https-port: %+v", st)
	}
}
