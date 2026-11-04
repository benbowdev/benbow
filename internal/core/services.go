// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/php"
	"github.com/getcovo/covo/internal/ports"
	"github.com/getcovo/covo/internal/store"
)

const (
	// EnvHTTPSPort sets the first HTTPS port tried when none is stored yet.
	EnvHTTPSPort     = "COVO_HTTPS_PORT"
	defaultHTTPSPort = 8443
	portSearchSpan   = 100

	settingHTTPSPort = "https_port"
	settingRunning   = "running"

	maxLogSize   = 10 << 20
	readyTimeout = 15 * time.Second
)

// Service names reported by status.
const caddyService = "caddy"

func phpService(minor string) string { return "php-" + minor }

// labelFor returns the launchd label of a service.
func (c *Core) labelFor(service string) string {
	return c.launchctl.Prefix + "." + strings.ReplaceAll(strings.ReplaceAll(service, "-", ""), ".", "")
}

func socketName(minor string) string { return "php" + strings.ReplaceAll(minor, ".", "") + ".sock" }

// httpsPort returns the stored HTTPS port, assigning one on first use: the
// first free port from 8443 (or $COVO_HTTPS_PORT) upward. Once stored it never
// changes on its own, so site URLs stay stable.
func (c *Core) httpsPort(ctx context.Context) (port int, assigned bool, err error) {
	v, ok, err := c.Store.Setting(ctx, settingHTTPSPort)
	if err != nil {
		return 0, false, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, false, fmt.Errorf("bad stored https_port %q: %w", v, err)
		}
		return port, false, nil
	}
	from := defaultHTTPSPort
	if env := os.Getenv(EnvHTTPSPort); env != "" {
		if from, err = strconv.Atoi(env); err != nil {
			return 0, false, errcode.New(errcode.InvalidInput, fmt.Errorf("%s: %w", EnvHTTPSPort, err))
		}
	}
	port, err = ports.FirstFree(from, portSearchSpan)
	if err != nil {
		return 0, false, err
	}
	if err := c.Store.SetSetting(ctx, settingHTTPSPort, strconv.Itoa(port)); err != nil {
		return 0, false, err
	}
	return port, true, nil
}

// checkPort fails if port is taken by anything but Covo's own Caddy.
func (c *Core) checkPort(ctx context.Context, port int) error {
	if ports.Free(port) {
		return nil
	}
	owners := ports.Owners(ctx, port)
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return err
	}
	if st, ok := loaded[c.labelFor(caddyService)]; ok && st.Running {
		for _, o := range owners {
			if o.PID == st.PID {
				return nil
			}
		}
	}
	msg := fmt.Sprintf("port %d is in use by %s", port, ports.Describe(owners))
	if alt, err := ports.FirstFree(port+1, portSearchSpan); err == nil {
		msg += fmt.Sprintf("; run `covo start --https-port %d` to use a free port", alt)
	}
	return errcode.Errorf(errcode.PortInUse, msg)
}

// setHTTPSPort changes the HTTPS port. It must be free.
func (c *Core) setHTTPSPort(ctx context.Context, port int) error {
	if port < 1024 || port > 65535 {
		return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf(
			"HTTPS port must be between 1024 and 65535, got %d; for 443 use `covo ports --https 443`", port))
	}
	cur, _, err := c.httpsPort(ctx)
	if err != nil {
		return err
	}
	if cur == port {
		return nil
	}
	if !ports.Free(port) {
		return errcode.Errorf(errcode.PortInUse,
			fmt.Sprintf("port %d is in use by %s", port, ports.Describe(ports.Owners(ctx, port))))
	}
	return c.Store.SetSetting(ctx, settingHTTPSPort, strconv.Itoa(port))
}

func (c *Core) caddyBinary(inst CaddyInstall) caddy.Binary {
	return caddy.Binary{Path: inst.Binary, DataDir: c.Layout.Data}
}

func (c *Core) caddyfilePath() string { return filepath.Join(c.Layout.Etc, "caddy", "Caddyfile") }

// renderCaddyfile needs the CA on disk (ensureCA).
func (c *Core) renderCaddyfile(ctx context.Context, p Ports, sites []store.Site, adminer *caddy.Adminer, phone *caddy.Phone) (string, error) {
	var mailUI int
	if on, err := c.mailEnabled(ctx); err != nil {
		return "", err
	} else if on {
		if _, mailUI, err = c.mailPorts(ctx); err != nil {
			return "", err
		}
	}
	cfg := caddy.Config{
		MailUIPort:  mailUI,
		Adminer:     adminer,
		AdminSocket: c.Layout.Socket("caddy-admin.sock"),
		HTTPSPort:   p.HTTPSListen,
		HTTPPort:    p.HTTPListen,
		PublicHTTPS: p.HTTPS,
		Phone:       phone,
		StorageDir:  filepath.Join(c.Layout.Data, "caddy"),
		LogFile:     filepath.Join(c.Layout.Logs, "caddy.log"),
		CA:          c.caddyCA(),
	}
	for _, s := range sites {
		settings, err := c.sitePHPSettings(ctx, s)
		if err != nil {
			return "", err
		}
		cfg.Sites = append(cfg.Sites, caddy.Site{
			Name:          s.Name,
			Domain:        s.Domain,
			Aliases:       s.Aliases,
			Root:          s.Path,
			FPMSocket:     c.Layout.Socket(socketName(s.PHPMinor)),
			AccessLog:     c.siteLogPath(s.Name, LogAccess),
			PHPValue:      settings.PHPValue(c.siteLogPath(s.Name, LogPHP)),
			ProbeDir:      c.probeDir(),
			ProbePHPValue: settings.PHPValue(""),
		})
	}
	body, err := caddy.Caddyfile(cfg)
	if err != nil {
		return "", err
	}
	fp, err := c.caFingerprint()
	if err != nil {
		return "", err
	}
	return body + "# ca root sha256: " + fp + "\n", nil
}

// writeCaddyfile validates and installs a new Caddyfile. If Caddy is running
// the config is reloaded; if the reload fails the previous file is restored.
func (c *Core) writeCaddyfile(ctx context.Context, bin caddy.Binary, body string, running bool) error {
	path := c.caddyfilePath()
	candidate := path + ".new"
	if _, err := fsutil.WriteAtomic(candidate, []byte(body), 0o644); err != nil {
		return err
	}
	defer os.Remove(candidate)
	if err := bin.Validate(ctx, candidate); err != nil {
		return err
	}
	prev, err := os.ReadFile(path)
	hadPrev := err == nil
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", path, err)
	}
	changed, err := fsutil.WriteAtomic(path, []byte(body), 0o644)
	if err != nil {
		return err
	}
	if !changed || !running {
		return nil
	}
	if err := bin.Reload(ctx, path, c.Layout.Socket("caddy-admin.sock")); err != nil {
		if hadPrev {
			_, _ = fsutil.WriteAtomic(path, prev, 0o644)
		} else {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}

// phpINIConfig is php.ini for minor without Xdebug (PHP-FPM adds it).
func (c *Core) phpINIConfig(ctx context.Context, v store.PHPVersion) (php.INIConfig, error) {
	sessions := filepath.Join(c.Layout.Data, "php", v.Minor, "sessions")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		return php.INIConfig{}, fmt.Errorf("create %s: %w", sessions, err)
	}
	mysqlSocket, mysqlPort, err := c.mysqlPHPSettings(ctx)
	if err != nil {
		return php.INIConfig{}, err
	}
	settings, err := c.phpSettings(ctx, v.Minor)
	if err != nil {
		return php.INIConfig{}, err
	}
	sendmail, sendmailPort, err := c.mailSendmail(ctx)
	if err != nil {
		return php.INIConfig{}, err
	}
	// Builds of old PHP versions ship some extensions as shared objects.
	exts, _ := filepath.Glob(filepath.Join(v.Path, "ext", "*.so"))
	return php.INIConfig{
		Extensions:   exts,
		Sendmail:     sendmail,
		SendmailPort: sendmailPort,
		Settings:     settings,
		ErrorLog:     filepath.Join(c.Layout.Logs, "php-"+v.Minor+"-error.log"),
		SessionDir:   sessions,
		Timezone:     localTimezone(),
		CAFile:       c.caBundlePath(),
		MySQLSocket:  mysqlSocket,
		MySQLPort:    mysqlPort,
	}, nil
}

// writePHPConfig writes php-fpm.conf and php.ini for minor and returns a hash
// of both and of the CA bundle, which goes into the agent so a change
// restarts FPM.
func (c *Core) writePHPConfig(ctx context.Context, v store.PHPVersion, bundleHash string) (string, error) {
	minor := v.Minor
	dir := filepath.Join(c.Layout.Etc, "php", minor)
	fpm, err := php.FPMConf(php.FPMConfig{
		Minor:    minor,
		Socket:   c.Layout.Socket(socketName(minor)),
		PIDFile:  c.Layout.Socket(strings.TrimSuffix(socketName(minor), ".sock") + ".pid"),
		ErrorLog: filepath.Join(c.Layout.Logs, "php-"+minor+"-fpm.log"),
	})
	if err != nil {
		return "", err
	}
	cfg, err := c.phpINIConfig(ctx, v)
	if err != nil {
		return "", err
	}
	if cfg.Xdebug, err = c.xdebugPath(ctx, v); err != nil {
		return "", err
	}
	ini, err := php.INI(cfg)
	if err != nil {
		return "", err
	}
	if _, err := fsutil.WriteAtomic(filepath.Join(dir, "php-fpm.conf"), []byte(fpm), 0o644); err != nil {
		return "", err
	}
	if _, err := fsutil.WriteAtomic(filepath.Join(dir, "php.ini"), []byte(ini), 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(fpm + "\x00" + ini + "\x00" + bundleHash))
	return hex.EncodeToString(sum[:8]), nil
}

func (c *Core) logPaths(service string) (string, string) {
	return filepath.Join(c.Layout.Logs, service+".out.log"), filepath.Join(c.Layout.Logs, service+".err.log")
}

func (c *Core) caddyAgent(bin caddy.Binary) launchd.Agent {
	out, errLog := c.logPaths(caddyService)
	return launchd.Agent{
		Label:     c.labelFor(caddyService),
		Program:   bin.Path,
		Args:      []string{"run", "--config", c.caddyfilePath(), "--adapter", "caddyfile"},
		Env:       bin.Env(),
		WorkDir:   c.Layout.Root,
		StdoutLog: out,
		StderrLog: errLog,
	}
}

func (c *Core) phpAgent(v store.PHPVersion, configHash string) launchd.Agent {
	dir := filepath.Join(c.Layout.Etc, "php", v.Minor)
	out, errLog := c.logPaths(phpService(v.Minor))
	return launchd.Agent{
		Label:   c.labelFor(phpService(v.Minor)),
		Program: filepath.Join(v.Path, "php-fpm"),
		Args:    []string{"--nodaemonize", "-c", filepath.Join(dir, "php.ini"), "--fpm-config", filepath.Join(dir, "php-fpm.conf")},
		// Not read by FPM: a changed value changes the plist, which makes
		// launchd.Apply restart the agent after a config change.
		// No scan directory: a build compiled with one (e.g. under
		// /opt/homebrew) must never load .ini files from outside Covo.
		Env:       map[string]string{"COVO_CONFIG_HASH": configHash, "PHP_INI_SCAN_DIR": ""},
		WorkDir:   c.Layout.Root,
		StdoutLog: out,
		StderrLog: errLog,
	}
}

// StartResult reports what start did.
type StartResult struct {
	HTTPSPort    int              `json:"https_port"`
	PortAssigned bool             `json:"port_assigned"`
	Changes      []launchd.Change `json:"changes"`
	Status
}

// Start installs whatever is missing, writes configs and makes the launchd
// agents match the configured sites. Running it again is a no-op.
// httpsPort, if non-zero, changes the HTTPS port first.
func (c *Core) Start(ctx context.Context, httpsPort int) (StartResult, error) {
	var res StartResult
	err := c.locked(ctx, func() error {
		if httpsPort != 0 {
			if err := c.setHTTPSPort(ctx, httpsPort); err != nil {
				return err
			}
		}
		if err := c.Store.SetSetting(ctx, settingRunning, "1"); err != nil {
			return err
		}
		var err error
		res, err = c.reconcile(ctx)
		return err
	})
	if err != nil {
		return res, err
	}
	res.Status, err = c.Status(ctx)
	return res, err
}

// enabledSites drops the sites that are turned off: Covo does not serve them
// and does not run PHP for them.
func enabledSites(sites []store.Site) []store.Site {
	return slices.DeleteFunc(slices.Clone(sites), func(s store.Site) bool { return !s.Enabled })
}

// reconcile brings services in line with the stored sites. Callers hold the lock.
func (c *Core) reconcile(ctx context.Context) (StartResult, error) {
	webPorts, assigned, err := c.ports(ctx)
	if err != nil {
		return StartResult{}, err
	}
	port := webPorts.HTTPSListen
	if err := c.checkPort(ctx, port); err != nil {
		return StartResult{}, err
	}
	if webPorts.HTTP != 0 {
		if err := c.checkPort(ctx, webPorts.HTTPListen); err != nil {
			return StartResult{}, err
		}
	}
	all, err := c.Store.ListSites(ctx)
	if err != nil {
		return StartResult{}, err
	}
	sites := enabledSites(all)
	caddyInst, err := c.ensureCaddy(ctx)
	if err != nil {
		return StartResult{}, err
	}
	bin := c.caddyBinary(caddyInst)
	if _, err := c.ensureCA(); err != nil {
		return StartResult{}, err
	}

	var agents []launchd.Agent
	var minors []string
	for _, s := range sites {
		if !slices.Contains(minors, s.PHPMinor) {
			minors = append(minors, s.PHPMinor)
		}
	}
	adminer, err := c.adminer(ctx)
	if err != nil {
		return StartResult{}, err
	}
	if adminer != nil && !slices.Contains(minors, adminerPHP) {
		minors = append(minors, adminerPHP)
	}
	slices.Sort(minors)
	var bundleHash string
	if len(minors) > 0 {
		if bundleHash, err = c.ensureCABundle(ctx); err != nil {
			return StartResult{}, err
		}
	}
	for _, minor := range minors {
		v, err := c.ensurePHP(ctx, minor)
		if err != nil {
			return StartResult{}, err
		}
		hash, err := c.writePHPConfig(ctx, v, bundleHash)
		if err != nil {
			return StartResult{}, err
		}
		agents = append(agents, c.phpAgent(v, hash))
	}

	names := make([]string, len(sites))
	for i, s := range sites {
		names[i] = s.Name
	}
	if err := c.ensureSiteLogDirs(names); err != nil {
		return StartResult{}, err
	}
	phone, phoneAgents, err := c.phoneConfig(ctx, sites, webPorts.HTTPS)
	if err != nil {
		return StartResult{}, err
	}
	body, err := c.renderCaddyfile(ctx, webPorts, sites, adminer, phone)
	if err != nil {
		return StartResult{}, err
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return StartResult{}, err
	}
	dnsAgents, dnsPort, err := c.dnsAgents(ctx, loaded)
	if err != nil {
		return StartResult{}, err
	}
	mysqlAgents, mysqlPort, err := c.mysqlAgents(ctx, loaded)
	if err != nil {
		return StartResult{}, err
	}
	mailAgents, mailSMTP, mailUI, err := c.mailAgents(ctx, loaded)
	if err != nil {
		return StartResult{}, err
	}
	pgAgents, pgPort, err := c.postgresAgents(ctx, loaded)
	if err != nil {
		return StartResult{}, err
	}
	redisAgents, redisPort, err := c.redisAgents(ctx, loaded)
	if err != nil {
		return StartResult{}, err
	}
	caddyRunning := loaded[c.labelFor(caddyService)].Running
	if err := c.writeCaddyfile(ctx, bin, body, caddyRunning); err != nil {
		return StartResult{}, err
	}
	agents = append([]launchd.Agent{c.caddyAgent(bin)}, agents...)
	agents = append(agents, dnsAgents...)
	agents = append(agents, mysqlAgents...)
	agents = append(agents, mailAgents...)
	agents = append(agents, pgAgents...)
	agents = append(agents, redisAgents...)
	agents = append(agents, phoneAgents...)

	rotated := c.rotateLogs()
	changes, err := c.launchctl.Apply(ctx, c.Layout.Launchd, agents)
	if err != nil {
		return StartResult{}, err
	}
	if err := c.reopenLogs(ctx, minors, rotated, changes); err != nil {
		return StartResult{}, err
	}
	var extra []readyCheck
	if pgPort != 0 {
		extra = append(extra, readyCheck{postgresService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(pgPort))})
	}
	if redisPort != 0 {
		extra = append(extra, readyCheck{redisService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(redisPort))})
	}
	if mailSMTP != 0 {
		extra = append(extra,
			readyCheck{mailService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mailSMTP))},
			readyCheck{mailService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mailUI))})
	}
	if err := c.waitReady(ctx, minors, port, len(sites) > 0 || adminer != nil || mailUI != 0, dnsPort, mysqlPort, extra...); err != nil {
		return StartResult{}, err
	}
	if err := c.syncForwards(ctx, webPorts); err != nil {
		return StartResult{}, err
	}
	return StartResult{HTTPSPort: webPorts.HTTPS, PortAssigned: assigned, Changes: changes}, nil
}

// waitReady waits until every socket accepts connections.
// readyCheck is an address a service must accept connections on.
type readyCheck struct {
	service, network, addr string
}

func (c *Core) waitReady(ctx context.Context, minors []string, port int, expectHTTPS bool, dnsPort, mysqlPort int, extra ...readyCheck) error {
	type check = readyCheck
	checks := []check{{caddyService, "unix", c.Layout.Socket("caddy-admin.sock")}}
	for _, m := range minors {
		checks = append(checks, check{phpService(m), "unix", c.Layout.Socket(socketName(m))})
	}
	if expectHTTPS {
		checks = append(checks, check{caddyService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port))})
	}
	if dnsPort != 0 {
		checks = append(checks, check{dnsService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(dnsPort))})
	}
	if mysqlPort != 0 {
		checks = append(checks, check{mysqlService, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(mysqlPort))})
	}
	checks = append(checks, extra...)
	deadline := time.Now().Add(readyTimeout)
	for _, ch := range checks {
		for {
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, ch.network, ch.addr)
			if err == nil {
				_ = conn.Close()
				break
			}
			if time.Now().After(deadline) {
				_, errLog := c.logPaths(ch.service)
				return errcode.Errorf(errcode.Service, fmt.Sprintf("%s did not become ready on %s: %v\n%s",
					ch.service, ch.addr, err, tail(errLog, 10)))
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("wait for %s: %w", ch.service, ctx.Err())
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	return nil
}

// StopResult reports what stop did.
type StopResult struct {
	Changes []launchd.Change `json:"changes"`
}

// Stop boots out every Covo agent. Running it again is a no-op.
func (c *Core) Stop(ctx context.Context) (StopResult, error) {
	var res StopResult
	err := c.locked(ctx, func() error {
		if err := c.Store.DeleteSetting(ctx, settingRunning); err != nil {
			return err
		}
		changes, err := c.launchctl.StopAll(ctx)
		res.Changes = changes
		if err != nil {
			return err
		}
		// 443 and 80 go back to whoever wants them while Covo is off.
		return c.clearForwards(ctx)
	})
	return res, err
}

// ServiceStatus is one service's state.
type ServiceStatus struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
}

// SiteStatus is a site as shown by status.
type SiteStatus struct {
	store.Site
	URL string `json:"url"`
	// Migration is set when Covo migrated the site from another tool (it
	// can be undone).
	Migration *store.Migration `json:"migration,omitempty"`
}

// CAStatus describes the local certificate authority.
type CAStatus struct {
	CertPath string `json:"cert_path"`
	Exists   bool   `json:"exists"`
	Trusted  bool   `json:"trusted"`
}

// Status is the overall state of Covo.
type Status struct {
	Running   bool `json:"running"`
	HTTPSPort int  `json:"https_port,omitempty"`
	// HTTPPort redirects to HTTPS; 0 is off.
	HTTPPort int             `json:"http_port,omitempty"`
	Home     string          `json:"home"`
	Services []ServiceStatus `json:"services"`
	Sites    []SiteStatus    `json:"sites"`
	CA       CAStatus        `json:"ca"`
	Domains  []DomainStatus  `json:"domains"`
	MySQL    MySQLStatus     `json:"mysql"`
	// AdminerURL is set while MySQL is enabled.
	AdminerURL string         `json:"adminer_url,omitempty"`
	Mail       MailStatus     `json:"mail"`
	Postgres   PostgresStatus `json:"postgres"`
	Redis      RedisStatus    `json:"redis"`
	// Phone is the site open on the phone, if any.
	Phone *PhoneStatus `json:"phone,omitempty"`
	// Groups organize the site list (see Site.GroupID).
	Groups []GroupInfo `json:"groups"`
	// Orphans are loaded Covo agents that no site needs any more.
	Orphans []string `json:"orphans,omitempty"`
}

// Status reports services, sites and the CA. It changes nothing.
func (c *Core) Status(ctx context.Context) (Status, error) {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return Status{}, err
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return Status{}, err
	}
	st := Status{Home: c.Layout.Root, Services: []ServiceStatus{}, Sites: []SiteStatus{}}
	if st.Groups, err = c.ListGroups(ctx); err != nil {
		return Status{}, err
	}
	if v, ok, err := c.Store.Setting(ctx, settingHTTPSPort); err == nil && ok {
		st.HTTPSPort, _ = strconv.Atoi(v)
	}
	if v, ok, err := c.Store.Setting(ctx, settingHTTPPort); err == nil && ok {
		st.HTTPPort, _ = strconv.Atoi(v)
	}
	services := []string{caddyService}
	for _, s := range sites {
		if svc := phpService(s.PHPMinor); s.Enabled && !slices.Contains(services, svc) {
			services = append(services, svc)
		}
		st.Sites = append(st.Sites, SiteStatus{Site: s, URL: siteURL(s, st.HTTPSPort)})
	}
	if migs, err := c.Store.ListMigrations(ctx); err == nil {
		for i := range st.Sites {
			if m, ok := migs[st.Sites[i].Name]; ok {
				st.Sites[i].Migration = &m
			}
		}
	}
	mysqlOn, err := c.mysqlEnabled(ctx)
	if err != nil {
		return Status{}, err
	}
	pgOn, err := c.postgresEnabled(ctx)
	if err != nil {
		return Status{}, err
	}
	redisOn, err := c.redisEnabled(ctx)
	if err != nil {
		return Status{}, err
	}
	if svc := phpService(adminerPHP); (mysqlOn || pgOn) && !slices.Contains(services, svc) {
		services = append(services, svc) // for Adminer
	}
	slices.Sort(services[1:])
	if ok, err := c.domainEnabled(ctx, suffixTest); err != nil {
		return Status{}, err
	} else if ok {
		services = append(services, dnsService)
	}
	mailOn, err := c.mailEnabled(ctx)
	if err != nil {
		return Status{}, err
	}
	if mysqlOn {
		services = append(services, mysqlService)
	}
	if pgOn {
		services = append(services, postgresService)
	}
	if redisOn {
		services = append(services, redisService)
	}
	if share, err := c.phoneShare(ctx); err != nil {
		return Status{}, err
	} else if share != nil {
		services = append(services, phoneService)
	}
	if mysqlOn || pgOn {
		st.AdminerURL = httpsURL(caddy.AdminerDomain, st.HTTPSPort) + "/"
	}
	if mailOn {
		services = append(services, mailService)
	}
	st.Running = true
	expected := map[string]bool{}
	for _, svc := range services {
		label := c.labelFor(svc)
		expected[label] = true
		ls := loaded[label]
		st.Services = append(st.Services, ServiceStatus{Name: svc, Label: label, Running: ls.Running, PID: ls.PID})
		st.Running = st.Running && ls.Running
	}
	for label := range loaded {
		if !expected[label] && !slices.Contains(c.launchctl.Keep, label) {
			st.Orphans = append(st.Orphans, label)
		}
	}
	slices.Sort(st.Orphans)
	if st.Domains, err = c.Domains(ctx); err != nil {
		return Status{}, err
	}
	if st.MySQL, err = c.mysqlStatus(ctx); err != nil {
		return Status{}, err
	}
	if st.Mail, err = c.mailStatus(ctx, st.HTTPSPort); err != nil {
		return Status{}, err
	}
	if st.Postgres, err = c.postgresStatus(ctx); err != nil {
		return Status{}, err
	}
	if st.Redis, err = c.redisStatus(ctx); err != nil {
		return Status{}, err
	}
	if st.Phone, err = c.Phone(ctx); err != nil {
		return Status{}, err
	}
	st.CA.CertPath = c.CACertPath()
	_, err = os.Stat(st.CA.CertPath)
	st.CA.Exists = err == nil
	if st.CA.Exists {
		if st.CA.Trusted, err = c.caTrusted(ctx); err != nil {
			return Status{}, err
		}
	}
	return st, nil
}

func siteURL(s store.Site, port int) string { return httpsURL(s.Domain, port) }

// httpsURL is https://host with the port, which 443 leaves out.
func httpsURL(host string, port int) string {
	switch port {
	case 0:
		port = defaultHTTPSPort
	case standardHTTPS:
		return "https://" + host
	}
	return "https://" + host + ":" + strconv.Itoa(port)
}

// rotateLogs keeps logs bounded: any log over maxLogSize is moved to
// <name>.1, replacing the previous one, and its name is returned. Caddy rolls
// its own log. Errors are ignored: logs must never block a start.
//
// After a rename a running process keeps writing to the old file until it
// reopens it: reopenLogs handles PHP-FPM; launchd's stdout/stderr files are
// only reopened when the agent restarts (Caddy and FPM write almost nothing
// there: their real logs are caddy.log and php-<v>-fpm.log).
func (c *Core) rotateLogs() []string {
	var rotated []string
	entries, err := os.ReadDir(c.Layout.Logs)
	if err != nil {
		return nil
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".log") || e.Name() == "caddy.log" {
			continue
		}
		if info, err := e.Info(); err == nil && info.Size() > maxLogSize {
			p := filepath.Join(c.Layout.Logs, e.Name())
			if os.Rename(p, p+".1") == nil {
				rotated = append(rotated, e.Name())
			}
		}
	}
	// Per-site PHP error logs: PHP opens them on every write, nothing to
	// reopen. Caddy rolls the access logs itself.
	logs, _ := filepath.Glob(filepath.Join(c.Layout.Logs, "sites", "*", "php-error.log"))
	for _, p := range logs {
		if info, err := os.Stat(p); err == nil && info.Size() > maxLogSize {
			_ = os.Rename(p, p+".1")
		}
	}
	return rotated
}

// reopenLogs sends SIGUSR1 to each PHP-FPM master whose log was rotated and
// that kept running, so it reopens its error_log. (PHP's own error_log is
// opened on every write and needs nothing.)
func (c *Core) reopenLogs(ctx context.Context, minors, rotated []string, changes []launchd.Change) error {
	if len(rotated) == 0 {
		return nil
	}
	unchanged := map[string]bool{}
	for _, ch := range changes {
		unchanged[ch.Label] = ch.Action == "unchanged"
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return err
	}
	for _, minor := range minors {
		label := c.labelFor(phpService(minor))
		if !slices.Contains(rotated, "php-"+minor+"-fpm.log") || !unchanged[label] {
			continue
		}
		if st := loaded[label]; st.Running && st.PID > 0 {
			if err := syscall.Kill(st.PID, syscall.SIGUSR1); err != nil {
				return errcode.New(errcode.Service, fmt.Errorf("signal %s to reopen logs: %w", label, err))
			}
		}
	}
	return nil
}

// tail returns the last n lines of a file, or "" if unreadable.
func tail(path string, n int) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// localTimezone returns the system IANA zone (e.g. Europe/Rome) for php.ini.
func localTimezone() string {
	target, err := os.Readlink("/etc/localtime")
	if err == nil {
		if _, zone, ok := strings.Cut(target, "zoneinfo/"); ok && zone != "" {
			return zone
		}
	}
	return "UTC"
}

// databasesEnabled reports whether MySQL or PostgreSQL is on (Adminer).
func (c *Core) databasesEnabled(ctx context.Context) (bool, error) {
	if on, err := c.mysqlEnabled(ctx); err != nil || on {
		return on, err
	}
	return c.postgresEnabled(ctx)
}
