// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package ports checks that TCP ports are free before Covo binds them.
package ports

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/errcode"
)

// Free reports whether port can be bound on both loopback addresses, which
// is where Covo listens.
func Free(port int) bool {
	for _, host := range []string{"127.0.0.1", "::1"} {
		l, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
		if err != nil {
			return false
		}
		_ = l.Close()
	}
	return true
}

// FreeDNS reports whether port is free for both UDP and TCP on 127.0.0.1,
// where Covo's DNS server listens.
func FreeDNS(port int) bool {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return false
	}
	_ = pc.Close()
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = l.Close()
	return true
}

// FirstFreeDNS returns the first port in [from, from+span) free for FreeDNS.
func FirstFreeDNS(from, span int) (int, error) {
	for p := from; p < from+span && p <= 65535; p++ {
		if FreeDNS(p) {
			return p, nil
		}
	}
	return 0, errcode.Newf(errcode.PortInUse, "no free DNS port between %d and %d", from, from+span-1)
}

// FirstFree returns the first free port in [from, from+span).
func FirstFree(from, span int) (int, error) {
	for p := from; p < from+span && p <= 65535; p++ {
		if Free(p) {
			return p, nil
		}
	}
	return 0, errcode.Newf(errcode.PortInUse, "no free port between %d and %d", from, from+span-1)
}

// Listener is a process listening on a port.
type Listener struct {
	PID     int    `json:"pid"`
	Command string `json:"command"`
}

// Owners returns the processes listening on port, as far as lsof can see
// (processes of other users may be invisible). Errors yield an empty list.
func Owners(ctx context.Context, port int) []Listener {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, "/usr/sbin/lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-Fpc").Output()
	return parseLsof(string(out))
}

// parseLsof parses lsof -F pc output: "p<pid>" then "c<command>" lines.
func parseLsof(out string) []Listener {
	var res []Listener
	seen := map[int]bool{}
	var cur Listener
	for line := range strings.Lines(out) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			cur = Listener{}
			cur.PID, _ = strconv.Atoi(line[1:])
		case 'c':
			cur.Command = line[1:]
			if !seen[cur.PID] {
				seen[cur.PID] = true
				res = append(res, cur)
			}
		}
	}
	return res
}

// Describe formats listeners for an error message.
func Describe(ls []Listener) string {
	if len(ls) == 0 {
		return "another process"
	}
	parts := make([]string, len(ls))
	for i, l := range ls {
		parts[i] = fmt.Sprintf("%s (pid %d)", l.Command, l.PID)
	}
	return strings.Join(parts, ", ")
}

// FreeAnywhere reports whether nothing listens on port on any address,
// privileged ports included: macOS lets any user bind the wildcard address
// to them. The probe does not set SO_REUSEADDR, so a listener on one
// specific address (e.g. 127.0.0.1) also counts.
func FreeAnywhere(port int) bool {
	for _, fam := range []int{unix.AF_INET, unix.AF_INET6} {
		fd, err := unix.Socket(fam, unix.SOCK_STREAM, 0)
		if err != nil {
			continue
		}
		var sa unix.Sockaddr = &unix.SockaddrInet4{Port: port}
		if fam == unix.AF_INET6 {
			sa = &unix.SockaddrInet6{Port: port}
		}
		err = unix.Bind(fd, sa)
		_ = unix.Close(fd)
		if errors.Is(err, unix.EADDRINUSE) {
			return false
		}
	}
	return true
}
