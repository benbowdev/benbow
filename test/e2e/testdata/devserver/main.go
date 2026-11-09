// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Command devserver stands in for a dev server Benbow starts: it listens on
// the port it is given, starts a child (like a bundler worker) and a
// grandchild in its own session (like a detached watcher), writes their
// pids to a file, reports its environment, and crashes on request.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

func main() {
	if os.Getenv("DEVSERVER_ROLE") != "" {
		time.Sleep(time.Hour)
		return
	}
	port := flag.String("port", os.Getenv("PORT"), "port")
	pids := flag.String("pids", "", "file for the pids")
	flag.Parse()
	start := func(setsid bool) int {
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "DEVSERVER_ROLE=1")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: setsid}
		if err := cmd.Start(); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		return cmd.Process.Pid
	}
	all := fmt.Sprintf("%d %d %d\n", os.Getpid(), start(false), start(true))
	if *pids != "" {
		if err := os.WriteFile(*pids, []byte(all), 0o644); err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
	}
	fmt.Println("devserver listening on", *port)
	http.HandleFunc("/crash", func(http.ResponseWriter, *http.Request) {
		fmt.Println("devserver crashing")
		os.Exit(3)
	})
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Println("devserver GET", r.URL.Path)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"port":                os.Getenv("PORT"),
			"flag_port":           *port,
			"node_extra_ca_certs": os.Getenv("NODE_EXTRA_CA_CERTS"),
			"benbow_vars":         benbowVars(),
			"app_url":             os.Getenv("APP_URL"),
			"database_url":        os.Getenv("DATABASE_URL"),
			"smtp_port":           os.Getenv("SMTP_PORT"),
			"my_secret":           os.Getenv("MY_SECRET"),
			"dir":                 mustWd(),
			"host":                r.Host,
		})
	})
	srv := &http.Server{Addr: "127.0.0.1:" + *port, ReadHeaderTimeout: 5 * time.Second}
	// Like laravel-vite-plugin: HTTPS with the certificate it is given.
	if cert, key := os.Getenv("VITE_DEV_SERVER_CERT"), os.Getenv("VITE_DEV_SERVER_KEY"); cert != "" {
		fmt.Println("devserver serving HTTPS for", os.Getenv("APP_URL"))
		fmt.Println(srv.ListenAndServeTLS(cert, key))
		os.Exit(1)
	}
	fmt.Println(srv.ListenAndServe())
	os.Exit(1)
}

// benbowVars lists Benbow's own variables that leaked into the app's
// environment (there should be none but the documented ones).
func benbowVars() string {
	var out []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "BENBOW_") && !strings.HasPrefix(kv, "BENBOW_APP_DIR=") && !strings.HasPrefix(kv, "BENBOW_APP_RUN=") {
			k, _, _ := strings.Cut(kv, "=")
			out = append(out, k)
		}
	}
	return strings.Join(out, ",")
}

func mustWd() string {
	wd, _ := os.Getwd()
	return wd
}
