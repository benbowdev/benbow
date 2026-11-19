// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/benbowdev/benbow/internal/caddy"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/store"
)

// Public link: one site at a time reachable from the internet through a
// Cloudflare quick tunnel (https://<random>.trycloudflare.com, no account),
// for payment providers' webhooks or showing a client. Benbow runs its own
// pinned cloudflared, with its own config and HOME so that nothing of the
// user's (~/.cloudflared) is read; cloudflared talks to Caddy over a Unix
// socket, never a TCP port. Caddy serves the site there under the public
// name cloudflared sends as Host, through the same prepend file as Open on
// phone: the database and files are never changed. The link closes by
// itself, and when Benbow stops; quick tunnel addresses change every time.

const (
	settingTunnel        = "tunnel_share"
	tunnelService        = "tunnel"
	tunnelDefaultMinutes = 60
	tunnelMaxMinutes     = 8 * 60
	tunnelFirstPort      = 20241 // cloudflared's own default metrics port
	tunnelUser           = "benbow"
	// tunnelWait is how long OpenTunnel waits for Cloudflare's address.
	tunnelWait = 45 * time.Second
)

// TunnelShare is the site open on a public link, as stored.
type TunnelShare struct {
	Site  string    `json:"site"`
	Until time.Time `json:"until"`
	// MetricsPort is cloudflared's local metrics server (127.0.0.1), which
	// tells the quick tunnel's address.
	MetricsPort int `json:"metrics_port"`
	// PasswordHash (bcrypt) asks visitors for user "benbow" and a password
	// shown once, when the link opens.
	PasswordHash string `json:"password_hash,omitempty"`
}

// TunnelStatus is what the app shows while a site has a public link.
type TunnelStatus struct {
	Site  string    `json:"site"`
	Until time.Time `json:"until"`
	// URL is "" until Cloudflare has given the tunnel its address.
	URL     string `json:"url"`
	Ready   bool   `json:"ready"`
	Minutes int    `json:"minutes_left"`
	// Protected is true when visitors need a password; User and Password
	// are only returned by the call that opened the link (the password is
	// not stored).
	Protected bool   `json:"protected"`
	User      string `json:"user,omitempty"`
	Password  string `json:"password,omitempty"`
	// LocalURL is the site's own address, which pages show as URL.
	LocalURL string `json:"local_url"`
}

func (c *Core) tunnelShare(ctx context.Context) (*TunnelShare, error) {
	v, ok, err := c.Store.Setting(ctx, settingTunnel)
	if err != nil || !ok {
		return nil, err
	}
	var s TunnelShare
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return nil, fmt.Errorf("read public link: %w", err)
	}
	if time.Now().After(s.Until) {
		return nil, nil
	}
	return &s, nil
}

// OpenTunnel gives a site a public link for minutes (default 60). Another
// site's link is closed and the new one gets a new address; for the same
// site the address stays and only the time (and whether a password is
// asked) changes. Benbow must be running. With password, visitors need user
// "benbow" and the password returned the first time (webhooks cannot send
// one); asking again keeps that password. It waits for Cloudflare's address
// for a while: if none came yet, URL is "" and Tunnel tells it later.
func (c *Core) OpenTunnel(ctx context.Context, name string, minutes int, password bool) (TunnelStatus, error) {
	if minutes == 0 {
		minutes = tunnelDefaultMinutes
	}
	if minutes < 1 || minutes > tunnelMaxMinutes {
		return TunnelStatus{}, errcode.Newf(errcode.InvalidInput, "minutes must be between 1 and %d", tunnelMaxMinutes)
	}
	var share TunnelShare
	var secret string
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if !site.Enabled {
			return errcode.Newf(errcode.Conflict, "%s is off: turn it on first", name)
		}
		if _, running, err := c.Store.Setting(ctx, settingRunning); err != nil {
			return err
		} else if !running {
			return errcode.Errorf(errcode.Conflict, "Benbow is stopped: start it first")
		}
		old, err := c.tunnelShare(ctx)
		if err != nil {
			return err
		}
		share = TunnelShare{Site: name, Until: time.Now().Add(time.Duration(minutes) * time.Minute)}
		// For another site the old cloudflared still holds its port, so the
		// new port differs, launchd starts a new tunnel and it gets a new
		// address: whoever had the old link never sees this site.
		if old != nil && old.Site == name {
			share.MetricsPort = old.MetricsPort
		} else if share.MetricsPort, err = firstFreeAnywhere(tunnelFirstPort); err != nil {
			return err
		}
		switch {
		case password && old != nil && old.Site == name && old.PasswordHash != "":
			share.PasswordHash = old.PasswordHash // more time, same password
		case password:
			secret = newTunnelPassword()
			// Caddy checks the hash on every request (each image and
			// stylesheet too): the lowest cost is plenty for a random
			// password of about 79 bits.
			h, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
			if err != nil {
				return fmt.Errorf("hash password: %w", err)
			}
			share.PasswordHash = string(h)
		}
		b, _ := json.Marshal(share)
		if err := c.Store.SetSetting(ctx, settingTunnel, string(b)); err != nil {
			return err
		}
		if _, err := c.reconcile(ctx); err != nil {
			if old != nil {
				b, _ := json.Marshal(old)
				_ = c.Store.SetSetting(ctx, settingTunnel, string(b))
			} else {
				_ = c.Store.DeleteSetting(ctx, settingTunnel)
			}
			_, _ = c.reconcile(ctx)
			return err
		}
		return nil
	})
	if err != nil {
		return TunnelStatus{}, err
	}
	st, err := c.waitTunnel(ctx, &share)
	if secret != "" {
		st.User, st.Password = tunnelUser, secret
	}
	return st, err
}

// newTunnelPassword is 16 letters and digits without look-alikes, easy to
// read out to a client.
func newTunnelPassword() string {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, 0, 16)
	for len(b) < 16 {
		var r [1]byte
		_, _ = rand.Read(r[:])
		if int(r[0]) < 256-256%len(alphabet) { // no bias toward the first letters
			b = append(b, alphabet[int(r[0])%len(alphabet)])
		}
	}
	return string(b[:4]) + "-" + string(b[4:8]) + "-" + string(b[8:12]) + "-" + string(b[12:])
}

// waitTunnel waits until cloudflared reports the tunnel's address and a
// connection to Cloudflare, or tunnelWait passes.
func (c *Core) waitTunnel(ctx context.Context, s *TunnelShare) (TunnelStatus, error) {
	deadline := time.Now().Add(tunnelWait)
	for {
		st, err := c.tunnelStatus(ctx, s)
		if err != nil || (st.URL != "" && st.Ready) || time.Now().After(deadline) {
			return st, err
		}
		select {
		case <-ctx.Done():
			return st, fmt.Errorf("wait for the public link: %w", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
}

// CloseTunnel closes the public link.
func (c *Core) CloseTunnel(ctx context.Context) error {
	return c.locked(ctx, func() error {
		if _, ok, err := c.Store.Setting(ctx, settingTunnel); err != nil || !ok {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingTunnel); err != nil {
			return err
		}
		_, err := c.applyIfRunning(ctx)
		return err
	})
}

// ExpireTunnel closes the public link once its time is up (the daemon calls
// it every 30 seconds). It reports whether it did.
func (c *Core) ExpireTunnel(ctx context.Context) (bool, error) {
	v, ok, err := c.Store.Setting(ctx, settingTunnel)
	if err != nil || !ok {
		return false, err
	}
	var s TunnelShare
	if json.Unmarshal([]byte(v), &s) == nil && time.Now().Before(s.Until) {
		return false, nil
	}
	return true, c.CloseTunnel(ctx)
}

// Tunnel returns the site with a public link, or nil.
func (c *Core) Tunnel(ctx context.Context) (*TunnelStatus, error) {
	s, err := c.tunnelShare(ctx)
	if err != nil || s == nil {
		return nil, err
	}
	st, err := c.tunnelStatus(ctx, s)
	return &st, err
}

// tunnelClient is short: status asks on every refresh of the app.
var tunnelClient = &http.Client{Timeout: time.Second}

func (c *Core) tunnelStatus(ctx context.Context, s *TunnelShare) (TunnelStatus, error) {
	st := TunnelStatus{Site: s.Site, Until: s.Until, Protected: s.PasswordHash != "",
		Minutes: max(0, int(time.Until(s.Until).Round(time.Minute).Minutes())),
	}
	if site, err := c.Store.Site(ctx, s.Site); err == nil {
		p, _, err := c.ports(ctx)
		if err != nil {
			return st, err
		}
		st.LocalURL = siteURL(site, p.HTTPS)
	}
	base := "http://127.0.0.1:" + strconv.Itoa(s.MetricsPort)
	var qt struct {
		Hostname string `json:"hostname"`
	}
	if getLocalJSON(ctx, base+"/quicktunnel", &qt) == nil && tunnelHost(qt.Hostname) {
		st.URL = "https://" + qt.Hostname
	}
	var ready struct {
		Status int `json:"status"`
	}
	st.Ready = st.URL != "" && getLocalJSON(ctx, base+"/ready", &ready) == nil && ready.Status == http.StatusOK &&
		tunnelResolves(ctx, qt.Hostname)
	return st, nil
}

// getLocalJSON reads cloudflared's metrics server. /ready answers 503 with
// a JSON body until a connection is up.
func getLocalJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("read %s: %w", url, err)
	}
	resp, err := tunnelClient.Do(req)
	if err != nil {
		return fmt.Errorf("read %s: %w", url, err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(v); err != nil {
		return fmt.Errorf("read %s: %w", url, err)
	}
	return nil
}

// resolved remembers the tunnel names already in public DNS.
var resolved sync.Map

// tunnelResolvesFunc asks Cloudflare's public resolver whether a new quick
// tunnel name is in DNS yet (it takes a few seconds). The Mac's own
// resolver is not asked: a name looked up too early would be remembered
// as missing for a while, and the link would not open. Tests replace it.
var tunnelResolvesFunc = func(ctx context.Context, host string) bool {
	r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, "1.1.1.1:53")
	}}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	addrs, err := r.LookupHost(ctx, host)
	return err == nil && len(addrs) > 0
}

func tunnelResolves(ctx context.Context, host string) bool {
	if _, ok := resolved.Load(host); ok {
		return true
	}
	if !tunnelResolvesFunc(ctx, host) {
		return false
	}
	resolved.Store(host, true)
	return true
}

// tunnelConfig returns how Caddy and launchd serve the public link, or nil.
// It writes cloudflared's config and the prepend file, and installs
// cloudflared.
func (c *Core) tunnelConfig(ctx context.Context, sites []store.Site, publicHTTPS int) (*caddy.Tunnel, []launchd.Agent, error) {
	s, err := c.tunnelShare(ctx)
	if err != nil || s == nil {
		return nil, nil, err
	}
	var site *store.Site
	for i := range sites {
		if sites[i].Name == s.Site {
			site = &sites[i]
		}
	}
	if site == nil {
		return nil, nil, nil
	}
	bin, err := c.ensureCloudflared(ctx)
	if err != nil {
		return nil, nil, err
	}
	sock := c.Layout.Socket("tunnel.sock")
	cfg := &caddy.Tunnel{Site: site.Name, Socket: sock, LocalURL: siteURL(*site, publicHTTPS)}
	if s.PasswordHash != "" {
		cfg.User, cfg.PasswordHash = tunnelUser, s.PasswordHash
	}
	if site.IsApp() {
		cfg.Upstream, cfg.UpstreamLocalHost = site.AppPort, site.AppHost == AppHostLocalhost
	} else {
		prepend, err := c.writePrepend()
		if err != nil {
			return nil, nil, err
		}
		settings, err := c.sitePHPSettings(ctx, *site)
		if err != nil {
			return nil, nil, err
		}
		cfg.Root, cfg.FPMSocket = site.Path, c.Layout.Socket(socketName(site.PHPMinor))
		cfg.PHPValue = settings.PHPValue(c.siteLogPath(site.Name, LogPHP)) + "\nauto_prepend_file=" + prepend
		cfg.Laravel = site.Framework == FrameworkLaravel
	}
	dir := filepath.Join(c.Layout.Data, "tunnel")
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, nil, fmt.Errorf("create %s: %w", home, err)
	}
	conf := filepath.Join(c.Layout.Etc, "cloudflared.yml")
	if _, err := fsutil.WriteAtomic(conf, []byte(cloudflaredConfig(sock)), 0o600); err != nil {
		return nil, nil, err
	}
	agent := launchd.Agent{
		Label:   c.labelFor(tunnelService),
		Program: bin,
		Args:    cloudflaredArgs(conf, s.MetricsPort),
		// Its own HOME: cloudflared looks for ~/.cloudflared and friends.
		Env:              map[string]string{"HOME": home},
		WorkDir:          dir,
		StdoutLog:        filepath.Join(c.Layout.Logs, "tunnel.log"),
		StderrLog:        filepath.Join(c.Layout.Logs, "tunnel.log"),
		ExitTimeout:      5,
		RestartOnFailure: true,
	}
	return cfg, []launchd.Agent{agent}, nil
}

// cloudflaredConfig sends every request to Caddy's socket. A quick tunnel
// still wants --url on the command line; the ingress rule wins over it.
func cloudflaredConfig(sock string) string {
	return "# Generated by Benbow. Do not edit: changes are overwritten.\n" +
		"no-autoupdate: true\n" +
		"ingress:\n" +
		"  - service: " + strconv.Quote("unix:"+sock) + "\n"
}

func cloudflaredArgs(conf string, metricsPort int) []string {
	return []string{
		"tunnel", "--no-autoupdate", "--config", conf,
		"--url", "http://localhost", // required for a quick tunnel, unused (ingress above)
		"--metrics", "127.0.0.1:" + strconv.Itoa(metricsPort),
		"--grace-period", "2s",
	}
}

// ensureCloudflared installs the pinned cloudflared and returns its binary.
func (c *Core) ensureCloudflared(ctx context.Context) (string, error) {
	art, err := c.pinned(ctx, "cloudflared", "")
	if err != nil {
		return "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", err
	}
	return filepath.Join(inst.Dir, "cloudflared"), nil
}

var tunnelHostRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$`)

// tunnelHost checks the name cloudflared reports.
func tunnelHost(h string) bool { return len(h) < 254 && tunnelHostRe.MatchString(h) }
