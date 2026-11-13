// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/benbowdev/benbow/internal/agents"
	"github.com/benbowdev/benbow/internal/ca"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/hostshelper"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/mysql"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/resolver"
	"github.com/benbowdev/benbow/internal/trust"
)

// knownEntries are the only names Benbow creates at the top of its home. A
// folder holding anything else is not deleted: BENBOW_HOME may point somewhere
// it should not.
var knownEntries = []string{
	"bin", "etc", "launchd", "data", "logs", "tmp", "run",
	"benbow.db", "benbow.db-wal", "benbow.db-shm", "benbow.db-journal", "benbow.lock", ".DS_Store",
}

// UninstallPlan lists what uninstall removes. Site folders are never touched.
type UninstallPlan struct {
	Agents []string     `json:"agents"`
	CA     []trust.Cert `json:"ca"`
	// Resolver lists Benbow's files in /etc/resolver: removing them takes an
	// administrator authorization.
	Resolver []string `json:"resolver"`
	// HostsHelper lists the privileged helper's files; removing them (and
	// Benbow's /etc/hosts lines) needs no password.
	HostsHelper []string `json:"hosts_helper"`
	// Keychain lists Benbow's items in the login keychain (database, server
	// and GitHub secrets).
	Keychain []string `json:"keychain"`
	Paths    []string `json:"paths"`
	Unknown  []string `json:"unknown,omitempty"`
	// AgentConfigs are the AI agents whose global configuration has Benbow's
	// MCP server; uninstall removes that entry only.
	AgentConfigs []AgentConfig `json:"agent_configs,omitempty"`
	// MigrationBackups are the folders holding the wp-config.php and .env
	// files saved before migrations: they go with the Benbow folder.
	MigrationBackups []string `json:"migration_backups,omitempty"`
}

// AgentConfig is an agent's configuration file with Benbow's MCP server.
type AgentConfig struct {
	Agent string `json:"agent"`
	Name  string `json:"name"`
	Path  string `json:"path"`
}

// UninstallResult reports what uninstall did.
type UninstallResult struct {
	UninstallPlan
	Done bool `json:"done"`
	// Warnings are steps that failed without stopping the uninstall.
	Warnings []string `json:"warnings,omitempty"`
}

// Uninstaller works without opening the Benbow home, so it also cleans up a
// partial, interrupted or corrupted installation.
type Uninstaller struct {
	Layout    paths.Layout
	Launchctl *launchd.Launchctl
	Keychain  *trust.Keychain
	CAName    string
	Resolver  resolver.Manager
	// Agents is where AI agents keep their configuration; nil leaves them
	// alone (test installations must never touch the real ones).
	Agents *agents.Env
}

// NewUninstaller resolves the environment without creating anything.
func NewUninstaller() (*Uninstaller, error) {
	layout, err := paths.Default()
	if err != nil {
		return nil, err
	}
	kc, err := trust.Login()
	if err != nil {
		return nil, err
	}
	u := &Uninstaller{
		Layout: layout, Launchctl: launchd.New(), Keychain: kc, CAName: ca.Name(),
		Resolver: newResolverManager(layout.Root, layout.Tmp),
	}
	if u.Launchctl.Prefix == launchd.DefaultPrefix || os.Getenv("BENBOW_AGENTS_HOME") != "" {
		env := agents.DefaultEnv()
		u.Agents = &env
	}
	return u, nil
}

// Plan computes what Uninstall would remove, changing nothing.
func (u *Uninstaller) Plan(ctx context.Context) (UninstallPlan, error) {
	plan := UninstallPlan{Agents: []string{}, CA: []trust.Cert{}, Resolver: []string{}, HostsHelper: []string{}, Keychain: []string{}, Paths: []string{}}
	loaded, err := u.Launchctl.List(ctx)
	if err != nil {
		return plan, err
	}
	for label := range loaded {
		plan.Agents = append(plan.Agents, label)
	}
	slices.Sort(plan.Agents)

	cas, err := trustedCAs(ctx, u.Keychain, u.CAName, caCertPaths(u.Layout))
	if err != nil {
		return plan, err
	}
	plan.CA = append(plan.CA, cas...)

	if _, external := helperClient(); !external {
		if _, err := os.Stat(helperMarker(u.Layout)); err == nil {
			plan.HostsHelper = append(plan.HostsHelper, "Benbow's lines in "+hostshelper.HostsPath, hostshelper.BinaryPath, hostshelper.PlistPath)
		}
	}
	kcs, err := benbowKeychains(u.Launchctl.Prefix)
	if err != nil {
		return plan, err
	}
	for _, k := range kcs {
		if ok, err := k.kc.AnyExists(ctx); err != nil {
			return plan, err
		} else if ok {
			plan.Keychain = append(plan.Keychain, k.what+" ("+k.kc.Service+")")
		}
	}
	for _, zone := range []string{suffixTest} {
		st, _, err := u.Resolver.State(zone)
		if err != nil {
			return plan, err
		}
		if st.Installed {
			plan.Resolver = append(plan.Resolver, st.Path)
		}
	}

	if u.Agents != nil {
		for _, a := range agents.Detect(ctx, *u.Agents) {
			if a.Connected {
				plan.AgentConfigs = append(plan.AgentConfigs, AgentConfig{Agent: a.ID, Name: a.Name, Path: a.ConfigPath})
			}
		}
	}
	plan.MigrationBackups = migrationBackups(u.Layout)

	if _, err := os.Lstat(u.Layout.Root); err == nil {
		plan.Paths = append(plan.Paths, u.Layout.Root)
		entries, err := os.ReadDir(u.Layout.Root)
		if err != nil {
			return plan, fmt.Errorf("read %s: %w", u.Layout.Root, err)
		}
		for _, e := range entries {
			if !slices.Contains(knownEntries, e.Name()) {
				plan.Unknown = append(plan.Unknown, e.Name())
			}
		}
	}
	// The socket fallback lives outside the home.
	if !strings.HasPrefix(u.Layout.Run, u.Layout.Root+string(filepath.Separator)) {
		if _, err := os.Lstat(u.Layout.Run); err == nil {
			plan.Paths = append(plan.Paths, u.Layout.Run)
		}
	}
	return plan, nil
}

// Uninstall stops every Benbow agent, removes the CA from the keychain and
// deletes the Benbow home. It is idempotent.
func (u *Uninstaller) Uninstall(ctx context.Context) (UninstallResult, error) {
	plan, err := u.Plan(ctx)
	if err != nil {
		return UninstallResult{UninstallPlan: plan}, err
	}
	res := UninstallResult{UninstallPlan: plan}
	if len(plan.Unknown) > 0 {
		return res, errcode.Newf(errcode.Conflict,
			"%s contains files Benbow did not create (%s); refusing to delete it",
			u.Layout.Root, strings.Join(plan.Unknown, ", "))

	}
	if err := checkRemovable(u.Layout.Root); err != nil {
		return res, err
	}

	if _, err := u.Launchctl.StopAll(ctx); err != nil {
		return res, err
	}
	if _, err := removeCAs(ctx, u.Keychain, u.CAName, caCertPaths(u.Layout)); err != nil {
		return res, err
	}
	for _, zone := range []string{suffixTest} {
		if _, err := u.Resolver.Remove(ctx, zone); err != nil {
			return res, err
		}
	}
	if _, err := removeHelper(ctx, u.Layout); err != nil {
		return res, err
	}
	// After the agents stopped: MySQL is no longer running.
	kcs, err := benbowKeychains(u.Launchctl.Prefix)
	if err != nil {
		return res, err
	}
	for _, k := range kcs {
		if _, err := k.kc.DeleteAll(ctx); err != nil {
			return res, err
		}
	}
	for _, a := range plan.AgentConfigs {
		if err := agents.Disconnect(ctx, *u.Agents, a.Agent); err != nil {
			res.Warnings = append(res.Warnings, fmt.Sprintf("Benbow's MCP server is still in %s: %v", a.Path, err))
		}
	}
	for _, p := range plan.Paths {
		if err := os.RemoveAll(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return res, fmt.Errorf("remove %s: %w", p, err)
		}
	}
	res.Done = true
	return res, nil
}

// checkRemovable refuses to delete obviously wrong roots.
func checkRemovable(root string) error {
	home, _ := os.UserHomeDir()
	clean := filepath.Clean(root)
	if clean == "/" || clean == filepath.Clean(home) || strings.Count(clean, string(filepath.Separator)) < 2 {
		return errcode.Newf(errcode.ForbiddenFolder, "refusing to delete %s", root)
	}
	return nil
}

// benbowKeychains are every login keychain service Benbow stores secrets under.
func benbowKeychains(prefix string) ([]struct {
	what string
	kc   mysql.Keychain
}, error) {
	my, err := mysqlKeychain(prefix)
	if err != nil {
		return nil, err
	}
	pg, err := postgresKeychain(prefix)
	if err != nil {
		return nil, err
	}
	remote, github, appEnv, legacy := my, my, my, my
	remote.Service, github.Service, appEnv.Service = prefix+".remote", prefix+".github", prefix+"."+appEnvService
	legacy.Service = prefix + "." + mysqlLegacy.service
	return []struct {
		what string
		kc   mysql.Keychain
	}{
		{"MySQL passwords: root, site users and read-only users", my},
		{"MySQL 5.7 passwords: root, site users and read-only users", legacy},
		{"PostgreSQL passwords: superuser and site users", pg},
		{"Passwords of production and staging servers", remote},
		{"GitHub token for blueprints", github},
		{"Variables of apps", appEnv},
	}, nil
}

// migrationBackups lists the per-site backup folders that hold files.
func migrationBackups(l paths.Layout) []string {
	dir := filepath.Join(l.Data, "backups")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "agents" {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if files, err := os.ReadDir(p); err == nil && len(files) > 0 {
			out = append(out, p)
		}
	}
	return out
}
