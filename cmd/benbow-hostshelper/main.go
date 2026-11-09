// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Command benbow-hostshelper is Benbow's privileged helper for .local domains
// and the standard ports.
// It is a separate, small binary (standard library, x/sys and Benbow's hosts
// package only): see package hostshelper for exactly what it may do.
//
// Benbow installs it as the LaunchDaemon dev.benbow.hostshelper with fixed
// arguments; only root can change them.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/benbowdev/benbow/internal/hostshelper"
)

type files []string

func (f *files) String() string     { return strings.Join(*f, ",") }
func (f *files) Set(v string) error { *f = append(*f, v); return nil }

func main() { os.Exit(run()) }

func run() int {
	var (
		uid     = flag.String("uid", "", "the only user id allowed to connect (required)")
		socket  = flag.String("socket", hostshelper.SocketPath, "Unix socket to listen on")
		hostsF  = flag.String("hosts", hostshelper.HostsPath, "hosts file")
		flush   = flag.Bool("flush", true, "flush the DNS cache after a change")
		label   = flag.String("label", "", "launchd label to unload after uninstall")
		pfctl   = flag.String("pfctl", hostshelper.PFCtl, `pfctl to run for port forwards ("" disables them)`)
		version = flag.Bool("version", false, "print the protocol version and exit")
		remove  files
	)
	flag.Var(&remove, "remove", "file to delete on uninstall (repeatable)")
	flag.Parse()
	if *version {
		fmt.Println(hostshelper.Version)
		return 0
	}
	allowed, err := strconv.ParseUint(*uid, 10, 32)
	if err != nil || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: benbow-hostshelper --uid <uid> [flags]")
		return 2
	}

	ln, err := hostshelper.Listen(*socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	srv := &hostshelper.Server{Config: hostshelper.Config{
		HostsPath:  *hostsF,
		AllowedUID: uint32(allowed),
		Flush:      *flush,
		SelfFiles:  remove,
		Label:      *label,
	}}
	if *pfctl != "" {
		srv.PF = hostshelper.RunPFCtl(*pfctl)
	}
	err = srv.Serve(ctx, ln)
	srv.Shutdown()
	_ = os.Remove(*socket)
	if err != nil && !errors.Is(err, hostshelper.ErrUninstalled) && ctx.Err() == nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}
