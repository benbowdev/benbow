// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/hostshelper"
	"github.com/benbowdev/benbow/internal/ports"
)

// Ports: the HTTPS port (and the optional HTTP port, which only redirects
// to HTTPS) are the ones in site addresses. Caddy never runs as root, so it
// cannot listen on 443 or 80 itself: for those it listens on a high port
// and Benbow's helper forwards loopback connections to it with a pf rule,
// only while Benbow is running.

const (
	settingHTTPPort        = "http_port"
	settingHTTPSListenPort = "https_listen_port"
	settingHTTPListenPort  = "http_listen_port"
	defaultHTTPListenPort  = 8080
	standardHTTPS          = 443
	standardHTTP           = 80
)

// Ports are Benbow's web ports.
type Ports struct {
	// HTTPS is the port in site addresses (443: no port in addresses).
	HTTPS int `json:"https_port"`
	// HTTPSListen is where Caddy listens: HTTPS itself, or a high port that
	// 443 is forwarded to.
	HTTPSListen int `json:"https_listen_port"`
	// HTTP redirects to HTTPS; 0 is off.
	HTTP       int `json:"http_port,omitempty"`
	HTTPListen int `json:"http_listen_port,omitempty"`
}

func (p Ports) forwards() []hostshelper.Forward {
	var fw []hostshelper.Forward
	if p.HTTPS != p.HTTPSListen {
		fw = append(fw, hostshelper.Forward{From: p.HTTPS, To: p.HTTPSListen})
	}
	if p.HTTP != 0 && p.HTTP != p.HTTPListen {
		fw = append(fw, hostshelper.Forward{From: p.HTTP, To: p.HTTPListen})
	}
	return fw
}

// PortsStatus is the ports and the state of their forwards.
type PortsStatus struct {
	Ports
	// Forwarded is true when 443 or 80 need Benbow's helper.
	Forwarded bool `json:"forwarded"`
	// Active are the forwards the helper has in place right now.
	Active []hostshelper.Forward `json:"active"`
	Error  string                `json:"error,omitempty"`
}

func validPublicPort(port, standard int, what string, allowOff bool) error {
	if (allowOff && port == 0) || port == standard || (port >= 1024 && port <= 65535) {
		return nil
	}
	return errcode.Newf(errcode.InvalidInput,
		"%s port must be %d or between 1024 and 65535, got %d", what, standard, port)

}

// ports returns the configured ports, assigning listen ports on first use.
func (c *Core) ports(ctx context.Context) (Ports, bool, error) {
	https, assigned, err := c.httpsPort(ctx)
	if err != nil {
		return Ports{}, false, err
	}
	p := Ports{HTTPS: https, HTTPSListen: https}
	if https < 1024 {
		if p.HTTPSListen, err = c.listenPort(ctx, settingHTTPSListenPort, defaultHTTPSPort); err != nil {
			return Ports{}, false, err
		}
	}
	if v, ok, err := c.Store.Setting(ctx, settingHTTPPort); err != nil {
		return Ports{}, false, err
	} else if ok {
		if p.HTTP, err = strconv.Atoi(v); err != nil {
			return Ports{}, false, fmt.Errorf("bad stored http_port %q: %w", v, err)
		}
	}
	p.HTTPListen = p.HTTP
	if p.HTTP != 0 && p.HTTP < 1024 {
		if p.HTTPListen, err = c.listenPort(ctx, settingHTTPListenPort, defaultHTTPListenPort); err != nil {
			return Ports{}, false, err
		}
	}
	return p, assigned, nil
}

// listenPort returns the stored high port behind a forwarded one, or picks
// the first free one from `from` and stores it.
func (c *Core) listenPort(ctx context.Context, setting string, from int) (int, error) {
	if v, ok, err := c.Store.Setting(ctx, setting); err != nil {
		return 0, err
	} else if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", setting, v, err)
		}
		return port, nil
	}
	port, err := ports.FirstFree(from, portSearchSpan)
	if err != nil {
		return 0, err
	}
	return port, c.Store.SetSetting(ctx, setting, strconv.Itoa(port))
}

// GetPorts returns the ports and their forwards.
func (c *Core) GetPorts(ctx context.Context) (PortsStatus, error) {
	p, _, err := c.ports(ctx)
	if err != nil {
		return PortsStatus{}, err
	}
	st := PortsStatus{Ports: p, Forwarded: len(p.forwards()) > 0, Active: []hostshelper.Forward{}}
	client, _ := helperClient()
	if res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus}); err == nil {
		st.Active = res.Forwards
	} else if st.Forwarded {
		st.Error = err.Error()
	}
	return st, nil
}

// SetPorts changes the HTTPS port and the HTTP redirect port (0: off). 443
// and 80 install Benbow's helper first (one administrator authorization);
// beforePrompt is called right before the macOS dialog. Every new port
// must be free.
func (c *Core) SetPorts(ctx context.Context, https, http int, beforePrompt func()) (PortsStatus, error) {
	if err := validPublicPort(https, standardHTTPS, "HTTPS", false); err != nil {
		return PortsStatus{}, err
	}
	if err := validPublicPort(http, standardHTTP, "HTTP", true); err != nil {
		return PortsStatus{}, err
	}
	if https == http {
		return PortsStatus{}, errcode.Errorf(errcode.InvalidInput, "HTTPS and HTTP need different ports")
	}
	err := c.locked(ctx, func() error {
		cur, _, err := c.ports(ctx)
		if err != nil {
			return err
		}
		for _, p := range []int{https, http} {
			if p == 0 || p == cur.HTTPS || p == cur.HTTP {
				continue
			}
			if err := c.checkPublicPort(ctx, p); err != nil {
				return err
			}
		}
		if https < 1024 || (http != 0 && http < 1024) {
			if _, err := c.installHelper(ctx, beforePrompt); err != nil {
				return err
			}
		}
		if err := c.Store.SetSetting(ctx, settingHTTPSPort, strconv.Itoa(https)); err != nil {
			return err
		}
		if http == 0 {
			err = c.Store.DeleteSetting(ctx, settingHTTPPort)
		} else {
			err = c.Store.SetSetting(ctx, settingHTTPPort, strconv.Itoa(http))
		}
		if err != nil {
			return err
		}
		if _, err := c.applyIfRunning(ctx); err != nil {
			// Back to the ports that worked.
			_ = c.Store.SetSetting(ctx, settingHTTPSPort, strconv.Itoa(cur.HTTPS))
			if cur.HTTP == 0 {
				_ = c.Store.DeleteSetting(ctx, settingHTTPPort)
			} else {
				_ = c.Store.SetSetting(ctx, settingHTTPPort, strconv.Itoa(cur.HTTP))
			}
			_, _ = c.applyIfRunning(ctx)
			return err
		}
		if len(cur.forwards()) == 0 {
			return nil
		}
		return c.releaseHelperIfUnused(ctx)
	})
	if err != nil {
		return PortsStatus{}, err
	}
	return c.GetPorts(ctx)
}

// checkPublicPort fails if port is taken by another program. A privileged
// port is checked on every address: a forward would take its loopback
// connections away from whoever listens there (e.g. MAMP on 80).
func (c *Core) checkPublicPort(ctx context.Context, port int) error {
	free := ports.Free(port)
	if port < 1024 {
		free = ports.FreeAnywhere(port)
	}
	if free {
		return nil
	}
	return errcode.Newf(errcode.PortInUse, "port %d is in use by %s", port, ports.Describe(ports.Owners(ctx, port)))
}

// syncForwards puts the forwards p needs in place (or removes them). A
// standard port another program took in the meantime is not forwarded: it
// stays with that program and the error says so.
func (c *Core) syncForwards(ctx context.Context, p Ports) error {
	want := p.forwards()
	client, _ := helperClient()
	if len(want) == 0 {
		res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus})
		if err != nil || len(res.Forwards) == 0 {
			return nil
		}
		_, err = client.Do(ctx, hostshelper.Request{Op: hostshelper.OpPorts})
		return wrapHelperErr(err)
	}
	res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus})
	if err != nil {
		return errcode.Newf(errcode.Service,
			"ports %s need Benbow's helper, which does not answer (%v); set the ports again in Settings or with `benbow ports`",
			describeForwards(want), err)

	}
	for _, f := range want {
		if slices.Contains(res.Forwards, f) {
			continue
		}
		if !ports.FreeAnywhere(f.From) {
			return errcode.Newf(errcode.PortInUse,
				"port %d is now in use by %s; Benbow leaves it alone: stop that program or choose another port",
				f.From, ports.Describe(ports.Owners(ctx, f.From)))

		}
	}
	_, err = client.Do(ctx, hostshelper.Request{Op: hostshelper.OpPorts, Forwards: want})
	return wrapHelperErr(err)
}

// clearForwards removes the forwards, if a helper has any.
func (c *Core) clearForwards(ctx context.Context) error {
	client, _ := helperClient()
	res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus})
	if err != nil || len(res.Forwards) == 0 {
		return nil
	}
	_, err = client.Do(ctx, hostshelper.Request{Op: hostshelper.OpPorts})
	return wrapHelperErr(err)
}

func wrapHelperErr(err error) error {
	if err == nil {
		return nil
	}
	return errcode.New(errcode.Service, err)
}

func describeForwards(fw []hostshelper.Forward) string {
	s := ""
	for i, f := range fw {
		if i > 0 {
			s += " and "
		}
		s += strconv.Itoa(f.From)
	}
	return s
}

// helperNeeded reports whether .local domains or standard ports use the
// helper.
func (c *Core) helperNeeded(ctx context.Context) (bool, error) {
	local, err := c.domainEnabled(ctx, suffixLocal)
	if err != nil || local {
		return local, err
	}
	p, _, err := c.ports(ctx)
	if err != nil {
		return false, err
	}
	return len(p.forwards()) > 0, nil
}

// releaseHelperIfUnused removes the helper once nothing needs it.
func (c *Core) releaseHelperIfUnused(ctx context.Context) error {
	needed, err := c.helperNeeded(ctx)
	if err != nil || needed {
		return err
	}
	if _, err := c.uninstallHelper(ctx); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
