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

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/trust"
)

// knownEntries are the only names Covo creates at the top of its home. A
// folder holding anything else is not deleted: COVO_HOME may point somewhere
// it should not.
var knownEntries = []string{
	"bin", "etc", "launchd", "data", "logs", "tmp", "run",
	"covo.db", "covo.db-wal", "covo.db-shm", "covo.db-journal", "covo.lock", ".DS_Store",
}

// UninstallPlan lists what uninstall removes. Site folders are never touched.
type UninstallPlan struct {
	Agents  []string     `json:"agents"`
	CA      []trust.Cert `json:"ca"`
	Paths   []string     `json:"paths"`
	Unknown []string     `json:"unknown,omitempty"`
}

// UninstallResult reports what uninstall did.
type UninstallResult struct {
	UninstallPlan
	Done bool `json:"done"`
}

// Uninstaller works without opening the Covo home, so it also cleans up a
// partial, interrupted or corrupted installation.
type Uninstaller struct {
	Layout    paths.Layout
	Launchctl *launchd.Launchctl
	Keychain  *trust.Keychain
	CAName    string
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
	return &Uninstaller{Layout: layout, Launchctl: launchd.New(), Keychain: kc, CAName: caddy.CAName()}, nil
}

func (u *Uninstaller) caCertPath() string {
	return filepath.Join(u.Layout.Data, "caddy", "pki", "authorities", "local", "root.crt")
}

// Plan computes what Uninstall would remove, changing nothing.
func (u *Uninstaller) Plan(ctx context.Context) (UninstallPlan, error) {
	plan := UninstallPlan{Agents: []string{}, CA: []trust.Cert{}, Paths: []string{}}
	loaded, err := u.Launchctl.List(ctx)
	if err != nil {
		return plan, err
	}
	for label := range loaded {
		plan.Agents = append(plan.Agents, label)
	}
	slices.Sort(plan.Agents)

	onDisk, err := trust.ReadCert(u.caCertPath())
	if err == nil {
		if ok, err := u.Keychain.Trusted(ctx, onDisk); err != nil {
			return plan, err
		} else if ok {
			plan.CA = append(plan.CA, onDisk)
		}
	} else {
		// CA file gone: fall back to the name.
		found, err := u.Keychain.Find(ctx, u.CAName)
		if err != nil {
			return plan, err
		}
		plan.CA = append(plan.CA, found...)
	}

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

// Uninstall stops every Covo agent, removes the CA from the keychain and
// deletes the Covo home. It is idempotent.
func (u *Uninstaller) Uninstall(ctx context.Context) (UninstallResult, error) {
	plan, err := u.Plan(ctx)
	if err != nil {
		return UninstallResult{UninstallPlan: plan}, err
	}
	res := UninstallResult{UninstallPlan: plan}
	if len(plan.Unknown) > 0 {
		return res, errcode.Errorf(errcode.Conflict, fmt.Sprintf(
			"%s contains files Covo did not create (%s); refusing to delete it",
			u.Layout.Root, strings.Join(plan.Unknown, ", ")))
	}
	if err := checkRemovable(u.Layout.Root); err != nil {
		return res, err
	}

	if _, err := u.Launchctl.StopAll(ctx); err != nil {
		return res, err
	}
	var onDisk *trust.Cert
	if c, err := trust.ReadCert(u.caCertPath()); err == nil {
		onDisk = &c
	}
	if _, err := u.Keychain.Remove(ctx, u.CAName, onDisk); err != nil {
		return res, err
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
		return errcode.Errorf(errcode.ForbiddenFolder, fmt.Sprintf("refusing to delete %s", root))
	}
	return nil
}
