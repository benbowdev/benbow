// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/ports"
	"github.com/getcovo/covo/internal/resolver"
)

// Domain suffixes. Covo only ever serves reserved suffixes, which its CA is
// constrained to.
const (
	suffixLocalhost = "localhost" // default, nothing to set up
	suffixTest      = "test"      // opt-in: resolver file + local DNS
	suffixLocal     = "local"     // opt-in: MAMP compatibility mode
)

// EnvResolverDir overrides /etc/resolver, so that tests never need
// administrator rights or touch the real file.
const EnvResolverDir = "COVO_RESOLVER_DIR"

const (
	settingDNSPort = "dns_port"
	defaultDNSPort = 53535
	dnsService     = "dns"
)

func settingDomain(zone string) string { return "domain_" + zone }

// domainRe is one or more lowercase DNS labels.
var domainRe = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]+$`)

// domainSuffix validates a site domain and returns its suffix.
func domainSuffix(domain string) (string, error) {
	if len(domain) > 253 || !domainRe.MatchString(domain) {
		return "", errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("domain %q must be lowercase letters, digits, dashes and dots, e.g. demo.localhost", domain))
	}
	suffix := domain[strings.LastIndexByte(domain, '.')+1:]
	if !slices.Contains([]string{suffixLocalhost, suffixTest, suffixLocal}, suffix) {
		return "", errcode.Errorf(errcode.InvalidInput,
			fmt.Sprintf("domain %q must end in .localhost, .test or .local: Covo only serves reserved local domains", domain))
	}
	return suffix, nil
}

func newResolverManager(home, tmp string) resolver.Manager {
	dir, run := resolver.SystemDir, resolver.Runner(resolver.Admin)
	if d := os.Getenv(EnvResolverDir); d != "" {
		dir, run = d, resolver.Direct
	}
	return resolver.Manager{Dir: dir, Home: home, Run: run, TmpDir: tmp}
}

// domainEnabled reports whether the opt-in suffix zone is enabled.
func (c *Core) domainEnabled(ctx context.Context, zone string) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingDomain(zone))
	return ok, err
}

// checkSiteDomain refuses a domain whose suffix is not available yet.
func (c *Core) checkSiteDomain(ctx context.Context, domain string) error {
	suffix, err := domainSuffix(domain)
	if err != nil {
		return err
	}
	switch suffix {
	case suffixTest:
		ok, err := c.domainEnabled(ctx, suffixTest)
		if err != nil {
			return err
		}
		if !ok {
			return errcode.Errorf(errcode.Unsupported,
				fmt.Sprintf("%s needs .test domains: run `covo domains enable test` first (one administrator authorization)", domain))
		}
	case suffixLocal:
		return errcode.Errorf(errcode.Unsupported,
			fmt.Sprintf("%s needs .local domains (MAMP compatibility mode), which is not available yet", domain))
	}
	return nil
}

// DomainStatus describes one opt-in domain suffix.
type DomainStatus struct {
	Zone     string         `json:"zone"`
	Enabled  bool           `json:"enabled"`
	DNSPort  int            `json:"dns_port,omitempty"`
	Resolver resolver.State `json:"resolver"`
}

// DomainResult reports what enable or disable did.
type DomainResult struct {
	DomainStatus
	Changed bool `json:"changed"`
	Applied bool `json:"applied"`
}

func checkZone(zone string) error {
	if zone == suffixTest {
		return nil
	}
	if zone == suffixLocal {
		return errcode.Errorf(errcode.Unsupported, ".local domains (MAMP compatibility mode) are not available yet")
	}
	return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("unknown domain suffix %q: only test can be enabled", zone))
}

func (c *Core) domainStatus(ctx context.Context, zone string) (DomainStatus, error) {
	st := DomainStatus{Zone: zone}
	var err error
	if st.Enabled, err = c.domainEnabled(ctx, zone); err != nil {
		return st, err
	}
	if v, ok, err := c.Store.Setting(ctx, settingDNSPort); err != nil {
		return st, err
	} else if ok {
		st.DNSPort, _ = strconv.Atoi(v)
	}
	st.Resolver, _, err = c.resolver.State(zone)
	return st, err
}

// dnsPort returns the stored DNS port, assigning the first free one from
// 53535 on first use. Like the HTTPS port it then never changes on its own:
// it is written in the resolver file.
func (c *Core) dnsPort(ctx context.Context) (int, error) {
	v, ok, err := c.Store.Setting(ctx, settingDNSPort)
	if err != nil {
		return 0, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", settingDNSPort, v, err)
		}
		return port, nil
	}
	port, err := ports.FirstFreeDNS(defaultDNSPort, portSearchSpan)
	if err != nil {
		return 0, err
	}
	return port, c.Store.SetSetting(ctx, settingDNSPort, strconv.Itoa(port))
}

// EnableDomain turns on an opt-in suffix (only "test" for now): it installs
// the resolver file, which takes one administrator authorization, and starts
// the DNS server if Covo is running. beforePrompt is called right before the
// macOS dialog, and not at all if nothing needs to change.
func (c *Core) EnableDomain(ctx context.Context, zone string, beforePrompt func()) (DomainResult, error) {
	if err := checkZone(zone); err != nil {
		return DomainResult{}, err
	}
	var res DomainResult
	err := c.locked(ctx, func() error {
		port, err := c.dnsPort(ctx)
		if err != nil {
			return err
		}
		m := c.resolver
		run := m.Run
		m.Run = func(ctx context.Context, prompt, cmd string) error {
			if beforePrompt != nil {
				beforePrompt()
			}
			return run(ctx, prompt, cmd)
		}
		if res.Changed, err = m.Install(ctx, zone, port); err != nil {
			return err
		}
		if err := c.Store.SetSetting(ctx, settingDomain(zone), "1"); err != nil {
			return err
		}
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.DomainStatus, err = c.domainStatus(ctx, zone)
		return err
	})
	return res, err
}

// DisableDomain turns off an opt-in suffix: it removes the resolver file (one
// administrator authorization) and stops the DNS server. It refuses while
// sites still use the suffix.
func (c *Core) DisableDomain(ctx context.Context, zone string, beforePrompt func()) (DomainResult, error) {
	if err := checkZone(zone); err != nil {
		return DomainResult{}, err
	}
	var res DomainResult
	err := c.locked(ctx, func() error {
		sites, err := c.Store.ListSites(ctx)
		if err != nil {
			return err
		}
		var using []string
		for _, s := range sites {
			if strings.HasSuffix(s.Domain, "."+zone) {
				using = append(using, s.Name)
			}
		}
		if len(using) > 0 {
			return errcode.Errorf(errcode.Conflict,
				fmt.Sprintf("sites still use .%s: %s; remove them first", zone, strings.Join(using, ", ")))
		}
		m := c.resolver
		run := m.Run
		m.Run = func(ctx context.Context, prompt, cmd string) error {
			if beforePrompt != nil {
				beforePrompt()
			}
			return run(ctx, prompt, cmd)
		}
		if res.Changed, err = m.Remove(ctx, zone); err != nil {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingDomain(zone)); err != nil {
			return err
		}
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.DomainStatus, err = c.domainStatus(ctx, zone)
		return err
	})
	return res, err
}

// Domains reports the opt-in suffixes.
func (c *Core) Domains(ctx context.Context) ([]DomainStatus, error) {
	st, err := c.domainStatus(ctx, suffixTest)
	if err != nil {
		return nil, err
	}
	return []DomainStatus{st}, nil
}

// selfBinary copies the running covo binary into the Covo home, so that the
// DNS agent never depends on where the CLI happens to be (a build folder,
// Downloads…). It returns the path and a short hash of the binary.
func (c *Core) selfBinary() (string, string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", "", fmt.Errorf("locate covo binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", "", fmt.Errorf("locate covo binary: %w", err)
	}
	body, err := os.ReadFile(exe)
	if err != nil {
		return "", "", fmt.Errorf("read covo binary: %w", err)
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:8])
	dst := filepath.Join(c.Layout.Bin, "covo", "covo")
	if cur, err := os.Open(dst); err == nil {
		h := sha256.New()
		_, _ = io.Copy(h, cur)
		_ = cur.Close()
		if hex.EncodeToString(h.Sum(nil)[:8]) == hash {
			return dst, hash, nil
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", fmt.Errorf("open %s: %w", dst, err)
	}
	if _, err := fsutil.WriteAtomic(dst, body, 0o755); err != nil {
		return "", "", err
	}
	return dst, hash, nil
}

func (c *Core) dnsAgent(bin, binHash string, port int, zones []string) launchd.Agent {
	out, errLog := c.logPaths(dnsService)
	args := []string{"dns", "serve", "--addr", "127.0.0.1:" + strconv.Itoa(port)}
	for _, z := range zones {
		args = append(args, "--zone", z)
	}
	return launchd.Agent{
		Label:   c.labelFor(dnsService),
		Program: bin,
		Args:    args,
		// A new covo binary restarts the agent.
		Env:       map[string]string{"COVO_BINARY_HASH": binHash},
		WorkDir:   c.Layout.Root,
		StdoutLog: out,
		StderrLog: errLog,
	}
}

// dnsAgents returns the DNS agent when an opt-in zone needs it, after
// checking that its port is free (or already Covo's).
func (c *Core) dnsAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	enabled, err := c.domainEnabled(ctx, suffixTest)
	if err != nil || !enabled {
		return nil, 0, err
	}
	port, err := c.dnsPort(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !loaded[c.labelFor(dnsService)].Running && !ports.FreeDNS(port) {
		return nil, 0, errcode.Errorf(errcode.PortInUse, fmt.Sprintf(
			"DNS port %d (127.0.0.1, written in %s) is in use by %s; run `covo domains disable test` and `covo domains enable test` to pick a free one",
			port, c.resolver.Path(suffixTest), ports.Describe(ports.Owners(ctx, port))))
	}
	bin, hash, err := c.selfBinary()
	if err != nil {
		return nil, 0, err
	}
	return []launchd.Agent{c.dnsAgent(bin, hash, port, []string{suffixTest})}, port, nil
}
