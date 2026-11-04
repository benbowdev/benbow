// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package postgres initializes Covo's PostgreSQL server and runs SQL on it.
// The superuser password never appears on a command line or in an
// environment variable: initdb reads it from a 0600 file and psql from a
// 0600 password file, both removed right after.
package postgres

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
)

// DefaultPort avoids 5432, used by Postgres.app, Homebrew and others.
const DefaultPort = 5433

// Superuser is the server's administrator role.
const Superuser = "postgres"

// secretFile writes content to a new 0600 file in dir and returns its path
// and a function that removes it.
func secretFile(dir, content string) (string, func(), error) {
	f, err := os.CreateTemp(dir, ".pg-*")
	if err != nil {
		return "", nil, fmt.Errorf("create password file: %w", err)
	}
	remove := func() { _ = os.Remove(f.Name()) }
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		remove()
		return "", nil, fmt.Errorf("chmod password file: %w", err)
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		remove()
		return "", nil, fmt.Errorf("write password file: %w", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", nil, fmt.Errorf("close password file: %w", err)
	}
	return f.Name(), remove, nil
}

// Initialize creates a data directory in dataDir (which must be empty or
// missing) with the superuser's password, scram-sha-256 authentication
// everywhere, UTF-8 and the C locale.
func Initialize(ctx context.Context, binDir, dataDir, password, tmpDir string) error {
	pwfile, remove, err := secretFile(tmpDir, password+"\n")
	if err != nil {
		return err
	}
	defer remove()
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, "initdb"),
		"-D", dataDir, "-U", Superuser, "--pwfile="+pwfile,
		"--auth-host=scram-sha-256", "--auth-local=scram-sha-256",
		"-E", "UTF8", "--locale=C", "--no-instructions")
	cmd.Env = cleanEnv()
	if out, err := cmd.CombinedOutput(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("initdb: %w: %s", err, strings.TrimSpace(string(out))))
	}
	return nil
}

// cleanEnv drops PG* variables of the user's shell, which would redirect
// Covo's tools to another server.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PG") {
			env = append(env, kv)
		}
	}
	return env
}

// passEscape escapes a field of a .pgpass line.
func passEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `:`, `\:`).Replace(s)
}

// Query runs sql as the superuser on db through 127.0.0.1:port and returns
// its unaligned, tuples-only output. sql goes to psql on stdin.
func Query(ctx context.Context, psql string, port int, password, db, sql, tmpDir string) (string, error) {
	pass, remove, err := secretFile(tmpDir, "127.0.0.1:"+strconv.Itoa(port)+":*:"+Superuser+":"+passEscape(password)+"\n")
	if err != nil {
		return "", err
	}
	defer remove()
	cmd := exec.CommandContext(ctx, psql, "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", Superuser, "-d", db,
		"-v", "ON_ERROR_STOP=1", "-X", "-q", "-A", "-t", "-f", "-")
	cmd.Env = append(cleanEnv(), "PGPASSFILE="+pass, "PGCONNECT_TIMEOUT=10")
	cmd.Stdin = strings.NewReader(sql)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "", errcode.New(errcode.Service, fmt.Errorf("psql: %w: %s", err, strings.TrimSpace(errs.String())))
	}
	return out.String(), nil
}

// Ident quotes an identifier.
func Ident(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Literal quotes a string literal.
func Literal(s string) string { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

// pgpassEnv writes a password file for the superuser on 127.0.0.1:port and
// returns the environment for Covo's PostgreSQL tools and a cleanup.
func pgpassEnv(port int, password, tmpDir string) ([]string, func(), error) {
	pass, remove, err := secretFile(tmpDir, "127.0.0.1:"+strconv.Itoa(port)+":*:"+Superuser+":"+passEscape(password)+"\n")
	if err != nil {
		return nil, nil, err
	}
	return append(cleanEnv(), "PGPASSFILE="+pass, "PGCONNECT_TIMEOUT=10"), remove, nil
}

// Dump writes db in pg_dump's custom format to path.
func Dump(ctx context.Context, binDir string, port int, password, db, path, tmpDir string) error {
	env, remove, err := pgpassEnv(port, password, tmpDir)
	if err != nil {
		return err
	}
	defer remove()
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, "pg_dump"), "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", Superuser,
		"-Fc", "-f", path, db)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("pg_dump %s: %w: %s", db, err, strings.TrimSpace(string(out))))
	}
	return nil
}

// Restore loads a custom-format dump into db (which must exist and be
// empty), creating its objects as role (the site's own user) when set.
func Restore(ctx context.Context, binDir string, port int, password, db, role, path, tmpDir string) error {
	env, remove, err := pgpassEnv(port, password, tmpDir)
	if err != nil {
		return err
	}
	defer remove()
	args := []string{"-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", Superuser, "-d", db, "--no-owner", "--no-privileges", "--exit-on-error"}
	if role != "" {
		args = append(args, "--role="+role)
	}
	cmd := exec.CommandContext(ctx, filepath.Join(binDir, "pg_restore"), append(args, path)...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("pg_restore %s: %w: %s", db, err, strings.TrimSpace(string(out))))
	}
	return nil
}

// QueryAs runs sql as role (with its password) on db, with extra server
// options (e.g. "-c statement_timeout=10s"), and returns tab-separated
// output with a first line of column names.
func QueryAs(ctx context.Context, psql string, port int, role, password, db, options, sql, tmpDir string) (string, error) {
	pass, remove, err := secretFile(tmpDir, "127.0.0.1:"+strconv.Itoa(port)+":*:"+passEscape(role)+":"+passEscape(password)+"\n")
	if err != nil {
		return "", err
	}
	defer remove()
	cmd := exec.CommandContext(ctx, psql, "-h", "127.0.0.1", "-p", strconv.Itoa(port), "-U", role, "-d", db,
		"-v", "ON_ERROR_STOP=1", "-X", "-q", "-A", "-F", "\t", "-P", "footer=off", "-f", "-")
	cmd.Env = append(cleanEnv(), "PGPASSFILE="+pass, "PGCONNECT_TIMEOUT=10", "PGOPTIONS="+options)
	cmd.Stdin = strings.NewReader(sql)
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "", errcode.New(errcode.InvalidInput, fmt.Errorf("psql: %s", strings.TrimSpace(errs.String())))
	}
	return out.String(), nil
}
