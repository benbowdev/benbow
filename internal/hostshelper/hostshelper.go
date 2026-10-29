// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package hostshelper is the privileged helper behind Covo's .local domains
// (MAMP compatibility mode), and its client.
//
// The helper runs as root, as the LaunchDaemon dev.getcovo.hostshelper. It
// does exactly four things, all on Covo's block in the hosts file (package
// hosts):
//
//   - status: report its version and the names in the block (read only);
//   - sync: make the block hold exactly the given .local names;
//   - clear: remove the block;
//   - uninstall: remove the block, then its own binary and plist, then
//     unload itself.
//
// It takes no paths, addresses or commands from requests: the hosts file,
// the IP addresses (127.0.0.1 and ::1) and its own files are fixed when it is
// installed. It only accepts connections from the user who installed it
// (checked with LOCAL_PEERCRED), one small JSON request per connection. It
// never runs a shell; the only programs it starts are dscacheutil and killall
// (to flush the DNS cache) and launchctl (to unload itself), with fixed
// arguments.
package hostshelper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/hosts"
)

// Version is the helper protocol version. A different version is reinstalled.
const Version = "1"

// Fixed locations of the installed helper.
const (
	Label      = "dev.getcovo.hostshelper"
	BinaryPath = "/Library/PrivilegedHelperTools/" + Label
	PlistPath  = "/Library/LaunchDaemons/" + Label + ".plist"
	SocketPath = "/var/run/" + Label + ".sock"
	HostsPath  = "/etc/hosts"
)

const (
	maxRequest  = 64 << 10
	connTimeout = 5 * time.Second
)

// Operations.
const (
	OpStatus    = "status"
	OpSync      = "sync"
	OpClear     = "clear"
	OpUninstall = "uninstall"
)

// Request is one call to the helper.
type Request struct {
	Op    string   `json:"op"`
	Names []string `json:"names,omitempty"`
}

// Response is the helper's answer.
type Response struct {
	OK      bool     `json:"ok"`
	Error   string   `json:"error,omitempty"`
	Version string   `json:"version"`
	Names   []string `json:"names"`
	Changed bool     `json:"changed,omitempty"`
}

// Config is fixed when the helper starts.
type Config struct {
	HostsPath  string
	AllowedUID uint32
	// Flush runs dscacheutil and killall after a change.
	Flush bool
	// SelfFiles are deleted by uninstall; Label, if set, is then unloaded.
	SelfFiles []string
	Label     string
}

// ErrUninstalled is returned by Serve after a successful uninstall.
var ErrUninstalled = errors.New("helper uninstalled")

// Server serves Config on a Unix socket.
type Server struct {
	Config
	mu sync.Mutex
}

// Serve accepts connections until ctx is done or uninstall succeeds.
func (s *Server) Serve(ctx context.Context, ln *net.UnixListener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("helper: %w", ctx.Err())
			}
			if errors.Is(err, net.ErrClosed) {
				return fmt.Errorf("helper: %w", err)
			}
			continue
		}
		if s.handle(conn) {
			_ = ln.Close()
			s.unload()
			return ErrUninstalled
		}
	}
}

// handle answers one connection and reports whether the helper uninstalled.
func (s *Server) handle(conn *net.UnixConn) (uninstalled bool) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(connTimeout))
	reply := func(r Response) {
		r.Version = Version
		if r.Names == nil {
			r.Names = []string{}
		}
		_ = json.NewEncoder(conn).Encode(r)
	}
	uid, err := peerUID(conn)
	if err != nil || uid != s.AllowedUID {
		reply(Response{Error: "forbidden: only the user who installed Covo's helper may use it"})
		return false
	}
	var req Request
	if err := json.NewDecoder(io.LimitReader(conn, maxRequest)).Decode(&req); err != nil {
		reply(Response{Error: "bad request"})
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	res, uninstalled := s.do(req)
	reply(res)
	return uninstalled
}

func (s *Server) do(req Request) (Response, bool) {
	fail := func(err error) (Response, bool) { return Response{Error: err.Error()}, false }
	switch req.Op {
	case OpStatus:
		if len(req.Names) > 0 {
			return fail(errors.New("status takes no names"))
		}
		content, _, err := readHosts(s.HostsPath)
		if err != nil {
			return fail(err)
		}
		names, err := hosts.Names(content)
		if err != nil {
			return fail(err)
		}
		return Response{OK: true, Names: names}, false
	case OpSync, OpClear, OpUninstall:
		names := req.Names
		if req.Op != OpSync {
			if len(names) > 0 {
				return fail(fmt.Errorf("%s takes no names", req.Op))
			}
			names = nil
		}
		changed, err := s.sync(names)
		if err != nil {
			return fail(err)
		}
		if req.Op != OpUninstall {
			now, _ := hosts.Normalize(names)
			return Response{OK: true, Names: now, Changed: changed}, false
		}
		for _, f := range s.SelfFiles {
			if err := os.Remove(f); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fail(fmt.Errorf("remove %s: %w", f, err))
			}
		}
		return Response{OK: true, Changed: true}, true
	default:
		return fail(fmt.Errorf("unknown operation %q", req.Op))
	}
}

// sync rewrites the hosts file atomically if the block must change.
func (s *Server) sync(names []string) (bool, error) {
	content, info, err := readHosts(s.HostsPath)
	if err != nil {
		return false, err
	}
	next, err := hosts.Sync(content, names)
	if err != nil {
		return false, err
	}
	if next == content {
		return false, nil
	}
	if len(next) > hosts.MaxSize {
		return false, errors.New("hosts file would be too large")
	}
	if err := writeAtomic(s.HostsPath, []byte(next), info); err != nil {
		return false, err
	}
	if s.Flush {
		_ = exec.Command("/usr/bin/dscacheutil", "-flushcache").Run()
		_ = exec.Command("/usr/bin/killall", "-HUP", "mDNSResponder").Run()
	}
	return true, nil
}

// unload removes the helper from launchd; it does not return when it works.
func (s *Server) unload() {
	if s.Label != "" {
		_ = exec.Command("/bin/launchctl", "bootout", "system/"+s.Label).Run()
	}
}

func readHosts(path string) (string, os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", nil, fmt.Errorf("stat hosts file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("hosts file is not a regular file")
	}
	if info.Size() > hosts.MaxSize {
		return "", nil, errors.New("hosts file is too large")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "", nil, fmt.Errorf("read hosts file: %w", err)
	}
	return string(b), info, nil
}

// writeAtomic replaces path with body, keeping its mode and owner: a
// temporary file in the same directory, fsync, rename.
func writeAtomic(path string, body []byte, orig os.FileInfo) error {
	suffix := make([]byte, 6)
	_, _ = rand.Read(suffix)
	tmp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".covo-"+hex.EncodeToString(suffix))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, orig.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create %s: %w", tmp, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(tmp)
		}
	}()
	if _, err := f.Write(body); err != nil {
		_ = f.Close()
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if err := f.Chmod(orig.Mode().Perm()); err != nil {
		_ = f.Close()
		return fmt.Errorf("chmod %s: %w", tmp, err)
	}
	if st, ok := orig.Sys().(*syscall.Stat_t); ok {
		if err := f.Chown(int(st.Uid), int(st.Gid)); err != nil {
			_ = f.Close()
			return fmt.Errorf("chown %s: %w", tmp, err)
		}
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("sync %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("replace %s: %w", path, err)
	}
	ok = true
	return nil
}

// peerUID returns the uid of the process on the other end of conn.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	var cred *unix.Xucred
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	})
	if err != nil {
		return 0, fmt.Errorf("peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("peer credentials: %w", credErr)
	}
	return cred.Uid, nil
}

// Listen creates the helper's socket at path, replacing a stale one, and
// opens it to every local user: Serve checks who connects.
func Listen(path string) (*net.UnixListener, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s exists and is not a socket", path)
		}
		_ = os.Remove(path)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return ln, nil
}

// Client talks to the helper.
type Client struct {
	Socket string
}

// Do sends one request.
func (c Client) Do(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return Response{}, fmt.Errorf("connect to Covo's helper: %w", err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return Response{}, fmt.Errorf("send to Covo's helper: %w", err)
	}
	var res Response
	if err := json.NewDecoder(io.LimitReader(conn, maxRequest)).Decode(&res); err != nil {
		return Response{}, fmt.Errorf("read from Covo's helper: %w", err)
	}
	if !res.OK {
		return res, fmt.Errorf("covo's helper: %s", res.Error)
	}
	return res, nil
}
