// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

// Package api serves the core over a local Unix socket: JSON over HTTP, the
// same envelope and error codes as `covo --json`. It is how the app (and
// later the MCP server) talk to the core; there is no TCP port.
//
// Only the user running the daemon may connect: the socket is 0600 in a 0700
// directory, and every connection's peer UID is checked as well.
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"golang.org/x/sys/unix"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/envelope"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/version"
)

// SocketName is the API socket's file name in the run directory.
const SocketName = "covo.sock"

// maxBody caps request bodies; every request is a few fields.
const maxBody = 1 << 20

// Backend is the part of the core the API exposes.
type Backend interface {
	Status(ctx context.Context) (core.Status, error)
	Start(ctx context.Context, httpsPort int) (core.StartResult, error)
	Stop(ctx context.Context) (core.StopResult, error)
	ListSites(ctx context.Context) ([]core.SiteStatus, error)
	AddSite(ctx context.Context, req core.AddSiteRequest) (core.SiteResult, error)
	UpdateSite(ctx context.Context, req core.UpdateSiteRequest) (core.SiteResult, error)
	RemoveSite(ctx context.Context, name string) (core.SiteResult, error)
	ListPHP(ctx context.Context) (core.PHPList, error)
	InstallPHP(ctx context.Context, spec string) (core.PHPInstall, error)
	GetPHPSettings(ctx context.Context, minor string) (core.PHPSettings, error)
	SetPHPSettings(ctx context.Context, minor string, values map[string]string) (core.PHPSettingsResult, error)
	Xdebug(ctx context.Context) ([]core.XdebugStatus, error)
	SetXdebug(ctx context.Context, minor string, on bool) (core.XdebugResult, error)
	Domains(ctx context.Context) ([]core.DomainStatus, error)
	EnableDomain(ctx context.Context, zone string, beforePrompt func()) (core.DomainResult, error)
	DisableDomain(ctx context.Context, zone string, beforePrompt func()) (core.DomainResult, error)
	MySQL(ctx context.Context) (core.MySQLStatus, error)
	EnableMySQL(ctx context.Context, port int) (core.MySQLResult, error)
	DisableMySQL(ctx context.Context) (core.MySQLResult, error)
	MySQLCredentials(ctx context.Context) (core.MySQLCredentials, error)
	Trust(ctx context.Context, beforePrompt func()) (core.TrustResult, error)
	Untrust(ctx context.Context) (core.TrustResult, error)
	ImportMAMP(ctx context.Context, opts core.ImportOptions) (core.ImportPlan, error)
}

// Handler routes /v1 requests to b.
func Handler(b Backend) http.Handler {
	mux := http.NewServeMux()
	get := func(pattern string, fn func(ctx context.Context) (any, error)) {
		mux.HandleFunc("GET "+pattern, func(w http.ResponseWriter, r *http.Request) {
			v, err := fn(r.Context())
			reply(w, v, err)
		})
	}
	// post reads the body for fn. A client hanging up does not cancel the
	// change half way: the core finishes it (or rolls it back) regardless.
	post := func(method, pattern string, fn func(r *http.Request, body []byte) (any, error)) {
		mux.HandleFunc(method+" "+pattern, func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
			if err != nil {
				reply(w, nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read request: %w", err)))
				return
			}
			v, err := fn(r.WithContext(context.WithoutCancel(r.Context())), body)
			reply(w, v, err)
		})
	}

	get("/v1/version", func(context.Context) (any, error) { return version.Get(), nil })
	get("/v1/status", func(ctx context.Context) (any, error) { return b.Status(ctx) })
	post("POST", "/v1/start", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			HTTPSPort int `json:"https_port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.Start(r.Context(), req.HTTPSPort)
	})
	post("POST", "/v1/stop", func(r *http.Request, body []byte) (any, error) {
		if err := decode(body, &struct{}{}); err != nil {
			return nil, err
		}
		return b.Stop(r.Context())
	})

	get("/v1/sites", func(ctx context.Context) (any, error) { return b.ListSites(ctx) })
	post("POST", "/v1/sites", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Name   string `json:"name"`
			Domain string `json:"domain"`
			PHP    string `json:"php"`
			Path   string `json:"path"`
			Force  bool   `json:"force"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.AddSite(r.Context(), core.AddSiteRequest{Name: req.Name, Domain: req.Domain, PHP: req.PHP, Path: req.Path, Force: req.Force})
	})
	post("PATCH", "/v1/sites/{name}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			PHP     string    `json:"php"`
			Path    string    `json:"path"`
			Domain  string    `json:"domain"`
			Enabled *bool     `json:"enabled"`
			Aliases *[]string `json:"aliases"`
			Force   bool      `json:"force"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.UpdateSite(r.Context(), core.UpdateSiteRequest{
			Name: r.PathValue("name"), PHP: req.PHP, Path: req.Path, Domain: req.Domain, Enabled: req.Enabled, Aliases: req.Aliases, Force: req.Force,
		})
	})
	post("DELETE", "/v1/sites/{name}", func(r *http.Request, _ []byte) (any, error) {
		return b.RemoveSite(r.Context(), r.PathValue("name"))
	})

	get("/v1/php", func(ctx context.Context) (any, error) { return b.ListPHP(ctx) })
	post("POST", "/v1/php", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Version string `json:"version"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.InstallPHP(r.Context(), req.Version)
	})
	mux.HandleFunc("GET /v1/php/{minor}/settings", func(w http.ResponseWriter, r *http.Request) {
		v, err := b.GetPHPSettings(r.Context(), r.PathValue("minor"))
		reply(w, v, err)
	})
	post("PATCH", "/v1/php/{minor}/settings", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			MemoryLimit       *string `json:"memory_limit"`
			UploadMaxFilesize *string `json:"upload_max_filesize"`
			MaxExecutionTime  *int    `json:"max_execution_time"`
			DisplayErrors     *bool   `json:"display_errors"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		values := map[string]string{}
		if req.MemoryLimit != nil {
			values["memory_limit"] = *req.MemoryLimit
		}
		if req.UploadMaxFilesize != nil {
			values["upload_max_filesize"] = *req.UploadMaxFilesize
		}
		if req.MaxExecutionTime != nil {
			values["max_execution_time"] = strconv.Itoa(*req.MaxExecutionTime)
		}
		if req.DisplayErrors != nil {
			values["display_errors"] = strconv.FormatBool(*req.DisplayErrors)
		}
		return b.SetPHPSettings(r.Context(), r.PathValue("minor"), values)
	})
	get("/v1/xdebug", func(ctx context.Context) (any, error) { return b.Xdebug(ctx) })
	post("PUT", "/v1/xdebug/{php}", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Enabled *bool `json:"enabled"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		if req.Enabled == nil {
			return nil, errcode.Errorf(errcode.InvalidInput, `missing "enabled"`)
		}
		return b.SetXdebug(r.Context(), r.PathValue("php"), *req.Enabled)
	})

	get("/v1/domains", func(ctx context.Context) (any, error) { return b.Domains(ctx) })
	post("POST", "/v1/domains/{zone}/enable", func(r *http.Request, _ []byte) (any, error) {
		return b.EnableDomain(r.Context(), r.PathValue("zone"), nil)
	})
	post("POST", "/v1/domains/{zone}/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisableDomain(r.Context(), r.PathValue("zone"), nil)
	})

	get("/v1/mysql", func(ctx context.Context) (any, error) { return b.MySQL(ctx) })
	post("POST", "/v1/mysql/enable", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			Port int `json:"port"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.EnableMySQL(r.Context(), req.Port)
	})
	post("POST", "/v1/mysql/disable", func(r *http.Request, _ []byte) (any, error) {
		return b.DisableMySQL(r.Context())
	})
	get("/v1/mysql/credentials", func(ctx context.Context) (any, error) { return b.MySQLCredentials(ctx) })

	post("POST", "/v1/trust", func(r *http.Request, _ []byte) (any, error) { return b.Trust(r.Context(), nil) })
	post("POST", "/v1/untrust", func(r *http.Request, _ []byte) (any, error) { return b.Untrust(r.Context()) })

	post("POST", "/v1/import/mamp", func(r *http.Request, body []byte) (any, error) {
		var req struct {
			DryRun    bool `json:"dry_run"`
			Localhost bool `json:"localhost"`
		}
		if err := decode(body, &req); err != nil {
			return nil, err
		}
		return b.ImportMAMP(r.Context(), core.ImportOptions{DryRun: req.DryRun, Localhost: req.Localhost})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		reply(w, nil, errcode.Errorf(errcode.NotFound, "no such endpoint: "+r.Method+" "+r.URL.Path))
	})
	return mux
}

// decode reads a JSON object strictly; an empty body is an empty object.
func decode(body []byte, v any) error {
	if len(body) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errcode.New(errcode.InvalidInput, fmt.Errorf("invalid request: %w", err))
	}
	if dec.More() {
		return errcode.Errorf(errcode.InvalidInput, "invalid request: trailing data")
	}
	return nil
}

func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(httpStatus(errcode.Code(err)))
		_ = envelope.Write(w, envelope.Error(err))
		return
	}
	_ = envelope.Write(w, envelope.OK(v))
}

// httpStatus maps an error code to an HTTP status. Clients should rely on
// the code in the body; the status is a convenience.
func httpStatus(code string) int {
	switch code {
	case errcode.InvalidInput, errcode.ForbiddenFolder:
		return http.StatusBadRequest
	case errcode.NotFound:
		return http.StatusNotFound
	case errcode.Conflict, errcode.PortInUse, errcode.ConfirmationRequired, errcode.Locked:
		return http.StatusConflict
	case errcode.Unsupported:
		return http.StatusUnprocessableEntity
	case errcode.Download, errcode.Checksum:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// Listen creates the API socket at path, readable only by the current user.
// A stale socket left by a crashed daemon is replaced; a live one is not.
func Listen(path string) (net.Listener, error) {
	if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
		_ = c.Close()
		return nil, errcode.Errorf(errcode.Conflict, "another Covo daemon is already listening on "+path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("remove stale socket: %w", err)
	}
	old := unix.Umask(0o177)
	ln, err := net.Listen("unix", path)
	unix.Umask(old)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return &uidListener{Listener: ln, uid: os.Getuid()}, nil
}

// uidListener drops connections from any other user.
type uidListener struct {
	net.Listener
	uid int
}

func (l *uidListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err //nolint:wrapcheck // http.Server inspects net.Error as is
		}
		if uid, err := peerUID(c); err == nil && uid == l.uid {
			return c, nil
		}
		_ = c.Close()
	}
}

func peerUID(c net.Conn) (int, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return -1, errors.New("not a Unix connection")
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return -1, fmt.Errorf("raw connection: %w", err)
	}
	uid := -1
	var credErr error
	err = raw.Control(func(fd uintptr) {
		cred, err := unix.GetsockoptXucred(int(fd), unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
		if err != nil {
			credErr = err
			return
		}
		uid = int(cred.Uid)
	})
	if err == nil {
		err = credErr
	}
	return uid, err
}

// Serve answers API requests on ln until ctx is cancelled, then waits up to
// shutdownWait for requests in flight.
func Serve(ctx context.Context, ln net.Listener, b Backend) error {
	srv := &http.Server{
		Handler:           Handler(b),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: installing PHP or importing sites takes minutes.
		BaseContext: func(net.Listener) context.Context { return context.WithoutCancel(ctx) },
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	sctx, cancel := context.WithTimeout(context.Background(), shutdownWait)
	defer cancel()
	if err := srv.Shutdown(sctx); err != nil {
		return fmt.Errorf("shut down API: %w", err)
	}
	return nil
}

const shutdownWait = 50 * time.Second

// Client returns an HTTP client that talks to the API socket at path; the
// host part of request URLs is ignored.
func Client(path string) *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}
