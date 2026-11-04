// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package remote reaches a site's copy on a server: SSH (the system's
// /usr/bin/ssh and /usr/bin/rsync, with the user's keys, agent and
// ~/.ssh/config), SFTP through ssh for hosts without a shell, and FTP or
// FTPS. Nothing here is installed: only macOS's own tools are run.
package remote

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"github.com/pkg/sftp"

	"github.com/getcovo/covo/internal/errcode"
)

// Transports.
const (
	SSH  = "ssh"
	SFTP = "sftp"
	FTP  = "ftp"
	FTPS = "ftps"
)

// Env overrides for tests: another ssh and rsync.
const (
	EnvSSH   = "COVO_SSH"
	EnvRsync = "COVO_RSYNC"
)

// Server is how to log in. Password, when set, comes from the keychain and
// stays in memory (ssh gets it through SSH_ASKPASS, never an argument).
type Server struct {
	Transport string
	Host      string
	Port      int
	User      string
	Password  string
	// AskPass is the program ssh asks for the password (Covo itself with
	// its askpass command), with AskPassEnv telling it which item to read.
	AskPass    string
	AskPassEnv []string
}

// File is an entry of a remote or local tree.
type File struct {
	Size    int64
	ModTime time.Time
	Dir     bool
}

// Files is a tree, by slash-separated path relative to its root.
type Files map[string]File

// FS is a remote file system.
type FS interface {
	// Walk lists dir recursively.
	Walk(ctx context.Context, dir string, skip func(rel string) bool) (Files, error)
	Get(ctx context.Context, p string, w io.Writer) error
	Put(ctx context.Context, p string, r io.Reader) error
	MkdirAll(ctx context.Context, dir string) error
	Remove(ctx context.Context, p string) error
	Close() error
}

func sshBinary() string {
	if s := os.Getenv(EnvSSH); s != "" {
		return s
	}
	return "/usr/bin/ssh"
}

func rsyncBinary() string {
	if s := os.Getenv(EnvRsync); s != "" {
		return s
	}
	return "/usr/bin/rsync"
}

// sshArgs are the options for every ssh run: new host keys are accepted
// the first time (and shown), a changed one is refused.
func (s Server) sshArgs() []string {
	args := []string{"-o", "StrictHostKeyChecking=accept-new", "-o", "ConnectTimeout=20", "-o", "ServerAliveInterval=30"}
	if s.Password == "" {
		args = append(args, "-o", "BatchMode=yes")
	} else {
		args = append(args, "-o", "PreferredAuthentications=password,keyboard-interactive,publickey", "-o", "NumberOfPasswordPrompts=1")
	}
	if s.Port != 0 {
		args = append(args, "-p", strconv.Itoa(s.Port))
	}
	return args
}

func (s Server) target() string {
	if s.User == "" {
		return s.Host
	}
	return s.User + "@" + s.Host
}

func (s Server) sshEnv() []string {
	env := os.Environ()
	if s.Password != "" && s.AskPass != "" {
		env = append(env, "SSH_ASKPASS="+s.AskPass, "SSH_ASKPASS_REQUIRE=force", "DISPLAY=covo:0")
		env = append(env, s.AskPassEnv...)
	}
	return env
}

// Quote quotes a string for a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// Run runs a shell command on an SSH server, with stdin and stdout.
func (s Server) Run(ctx context.Context, command string, stdin io.Reader, stdout io.Writer) error {
	if s.Transport != SSH {
		return errcode.Errorf(errcode.Unsupported, "commands need SSH")
	}
	var stderr strings.Builder
	// "--": the target is never read as an option.
	args := append(s.sshArgs(), "--", s.target(), command)
	cmd := exec.CommandContext(ctx, sshBinary(), args...)
	cmd.Env = s.sshEnv()
	cmd.Stdin, cmd.Stdout, cmd.Stderr = stdin, stdout, &stderr
	if err := cmd.Run(); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("ssh %s: %w: %s", s.Host, err, strings.TrimSpace(stderr.String())))
	}
	return nil
}

// Rsync copies src to dst with the system rsync over ssh (one of them is
// host:path). With dryRun nothing changes and the changed files are
// returned.
func (s Server) Rsync(ctx context.Context, src, dst string, excludes []string, dryRun bool) ([]string, error) {
	shell := append([]string{sshBinary()}, s.sshArgs()...)
	quoted := make([]string, len(shell))
	for i, a := range shell {
		quoted[i] = Quote(a)
	}
	args := []string{"-rlt", "-z", "--itemize-changes", "-e", strings.Join(quoted, " ")}
	if dryRun {
		args = append(args, "-n")
	}
	for _, e := range excludes {
		args = append(args, "--exclude", e)
	}
	args = append(args, "--", src, dst)
	var out, stderr strings.Builder
	cmd := exec.CommandContext(ctx, rsyncBinary(), args...)
	cmd.Env = s.sshEnv()
	cmd.Stdout, cmd.Stderr = &out, &stderr
	if err := cmd.Run(); err != nil {
		return nil, errcode.New(errcode.Service, fmt.Errorf("rsync: %w: %s", err, strings.TrimSpace(stderr.String())))
	}
	var changed []string
	for _, l := range strings.Split(out.String(), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && (strings.HasPrefix(f[0], ">f") || strings.HasPrefix(f[0], "<f")) {
			changed = append(changed, strings.Join(f[1:], " "))
		}
	}
	return changed, nil
}

// Open connects to the server's files (SFTP through ssh, or FTP/FTPS).
func (s Server) Open(ctx context.Context) (FS, error) {
	switch s.Transport {
	case SSH, SFTP:
		args := append(s.sshArgs(), "-s", "--", s.target(), "sftp")
		cmd := exec.CommandContext(ctx, sshBinary(), args...)
		cmd.Env = s.sshEnv()
		var stderr strings.Builder
		cmd.Stderr = &stderr
		w, err := cmd.StdinPipe()
		if err != nil {
			return nil, fmt.Errorf("sftp: %w", err)
		}
		r, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("sftp: %w", err)
		}
		if err := cmd.Start(); err != nil {
			return nil, errcode.New(errcode.Service, fmt.Errorf("start ssh: %w", err))
		}
		c, err := sftp.NewClientPipe(r, w)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, errcode.New(errcode.Service, fmt.Errorf("sftp %s: %w: %s", s.Host, err, strings.TrimSpace(stderr.String())))
		}
		return &sftpFS{c: c, cmd: cmd}, nil
	case FTP, FTPS:
		port := s.Port
		if port == 0 {
			port = 21
		}
		opts := []ftp.DialOption{ftp.DialWithContext(ctx), ftp.DialWithTimeout(30 * time.Second)}
		if s.Transport == FTPS {
			opts = append(opts, ftp.DialWithExplicitTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}))
		}
		c, err := ftp.Dial(s.Host+":"+strconv.Itoa(port), opts...)
		if err != nil {
			return nil, errcode.New(errcode.Service, fmt.Errorf("connect to %s: %w", s.Host, err))
		}
		if err := c.Login(s.User, s.Password); err != nil {
			_ = c.Quit()
			return nil, errcode.New(errcode.Service, fmt.Errorf("log in to %s: %w", s.Host, err))
		}
		return &ftpFS{c: c}, nil
	}
	return nil, errcode.Newf(errcode.InvalidInput, "unknown transport %q (ssh, sftp, ftp, ftps)", s.Transport)
}

type sftpFS struct {
	c   *sftp.Client
	cmd *exec.Cmd
}

func (f *sftpFS) Walk(_ context.Context, dir string, skip func(string) bool) (Files, error) {
	out := Files{}
	w := f.c.Walk(dir)
	for w.Step() {
		if err := w.Err(); err != nil {
			if w.Path() == dir {
				return nil, fmt.Errorf("list %s: %w", dir, err)
			}
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(w.Path(), dir), "/")
		if rel == "" {
			continue
		}
		if skip != nil && skip(rel) {
			if w.Stat().IsDir() {
				w.SkipDir()
			}
			continue
		}
		st := w.Stat()
		out[rel] = File{Size: st.Size(), ModTime: st.ModTime(), Dir: st.IsDir()}
	}
	return out, nil
}

func (f *sftpFS) Get(_ context.Context, p string, w io.Writer) error {
	r, err := f.c.Open(p)
	if err != nil {
		return fmt.Errorf("open %s: %w", p, err)
	}
	defer r.Close()
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("download %s: %w", p, err)
	}
	return nil
}

func (f *sftpFS) Put(_ context.Context, p string, r io.Reader) error {
	w, err := f.c.Create(p)
	if err != nil {
		return fmt.Errorf("create %s: %w", p, err)
	}
	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		return fmt.Errorf("upload %s: %w", p, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	return nil
}

func (f *sftpFS) MkdirAll(_ context.Context, dir string) error {
	if err := f.c.MkdirAll(dir); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	return nil
}

func (f *sftpFS) Remove(_ context.Context, p string) error {
	if err := f.c.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}

func (f *sftpFS) Close() error {
	_ = f.c.Close()
	_ = f.cmd.Process.Kill()
	_ = f.cmd.Wait()
	return nil
}

type ftpFS struct{ c *ftp.ServerConn }

func (f *ftpFS) Walk(_ context.Context, dir string, skip func(string) bool) (Files, error) {
	out := Files{}
	w := f.c.Walk(dir)
	for w.Next() {
		if err := w.Err(); err != nil {
			if w.Path() == dir {
				return nil, fmt.Errorf("list %s: %w", dir, err)
			}
			continue
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(w.Path(), dir), "/")
		if rel == "" {
			continue
		}
		e := w.Stat()
		isDir := e.Type == ftp.EntryTypeFolder
		if skip != nil && skip(rel) {
			if isDir {
				w.SkipDir()
			}
			continue
		}
		out[rel] = File{Size: int64(e.Size), ModTime: e.Time, Dir: isDir}
	}
	return out, nil
}

func (f *ftpFS) Get(_ context.Context, p string, w io.Writer) error {
	r, err := f.c.Retr(p)
	if err != nil {
		return fmt.Errorf("download %s: %w", p, err)
	}
	defer r.Close()
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("download %s: %w", p, err)
	}
	return nil
}

func (f *ftpFS) Put(_ context.Context, p string, r io.Reader) error {
	if err := f.c.Stor(p, r); err != nil {
		return fmt.Errorf("upload %s: %w", p, err)
	}
	return nil
}

func (f *ftpFS) MkdirAll(_ context.Context, dir string) error {
	cur := ""
	for _, part := range strings.Split(strings.Trim(dir, "/"), "/") {
		cur += "/" + part
		_ = f.c.MakeDir(cur) // exists already: fine
	}
	return nil
}

func (f *ftpFS) Remove(_ context.Context, p string) error {
	if err := f.c.Delete(p); err != nil {
		return fmt.Errorf("remove %s: %w", p, err)
	}
	return nil
}

func (f *ftpFS) Close() error { return f.c.Quit() } //nolint:wrapcheck // closing

// LocalFiles lists a local tree like Walk.
func LocalFiles(root string, skip func(string) bool) (Files, error) {
	out := Files{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == root {
				return err
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished meanwhile
		}
		out[rel] = File{Size: info.Size(), ModTime: info.ModTime(), Dir: d.IsDir()}
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("list %s: %w", root, err)
	}
	return out, nil
}

// Changed lists the files of src that dst lacks or has with another size,
// or an older time (FTP times are only to the minute: two minutes of
// tolerance).
func Changed(src, dst Files) []string {
	var out []string
	for rel, f := range src {
		if f.Dir {
			continue
		}
		d, ok := dst[rel]
		if !ok || d.Dir || d.Size != f.Size || f.ModTime.Sub(d.ModTime) > 2*time.Minute {
			out = append(out, rel)
		}
	}
	return out
}

// Skipper returns a filter for rsync-style exclude patterns: a pattern
// matches a path's name or the whole relative path.
func Skipper(excludes []string) func(string) bool {
	return func(rel string) bool {
		base := path.Base(rel)
		for _, e := range excludes {
			e = strings.TrimSuffix(e, "/")
			if ok, _ := path.Match(e, base); ok {
				return true
			}
			if ok, _ := path.Match(strings.TrimPrefix(e, "/"), rel); ok {
				return true
			}
		}
		return false
	}
}
