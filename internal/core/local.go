// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/hostshelper"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/resolver"
)

// EnvHostsHelperSocket points Covo at a helper it does not install or
// remove itself: tests run one as the user on a temporary hosts file.
const EnvHostsHelperSocket = "COVO_HOSTS_HELPER_SOCKET"

// helperBinaryName is the helper's file name next to the covo binary.
const helperBinaryName = "covo-hostshelper"

// HelperStatus describes the privileged hosts helper.
type HelperStatus struct {
	Installed bool     `json:"installed"`
	Running   bool     `json:"running"`
	Version   string   `json:"version,omitempty"`
	Names     []string `json:"names"`
	Error     string   `json:"error,omitempty"`
}

// helperMarker records, in the Covo home, that this home installed the
// system helper. Uninstall only touches the helper when it is there.
func helperMarker(l paths.Layout) string { return filepath.Join(l.Data, "hostshelper.installed") }

func helperClient() (hostshelper.Client, bool) {
	if s := os.Getenv(EnvHostsHelperSocket); s != "" {
		return hostshelper.Client{Socket: s}, true
	}
	return hostshelper.Client{Socket: hostshelper.SocketPath}, false
}

func (c *Core) helperStatus(ctx context.Context) *HelperStatus {
	st := &HelperStatus{Names: []string{}}
	client, external := helperClient()
	if external {
		st.Installed = true
	} else {
		_, err := os.Stat(hostshelper.PlistPath)
		st.Installed = err == nil
	}
	res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus})
	if err != nil {
		if st.Installed {
			st.Error = err.Error()
		}
		return st
	}
	st.Running, st.Version, st.Names = true, res.Version, res.Names
	return st
}

// localNames lists the .local domains and aliases of all sites.
func (c *Core) localNames(ctx context.Context) ([]string, error) {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for _, s := range sites {
		for _, d := range append([]string{s.Domain}, s.Aliases...) {
			if strings.HasSuffix(d, "."+suffixLocal) {
				names = append(names, d)
			}
		}
	}
	slices.Sort(names)
	return names, nil
}

// syncLocal makes Covo's /etc/hosts block match the .local sites. It is a
// no-op unless .local domains are enabled.
func (c *Core) syncLocal(ctx context.Context) error {
	enabled, err := c.domainEnabled(ctx, suffixLocal)
	if err != nil || !enabled {
		return err
	}
	names, err := c.localNames(ctx)
	if err != nil {
		return err
	}
	client, _ := helperClient()
	if _, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpSync, Names: names}); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("%w; run `covo domains enable local` to repair the helper", err))
	}
	return nil
}

// installHelper installs (or upgrades) the privileged hosts helper with one
// administrator authorization. It does nothing when the right version is
// already running.
func (c *Core) installHelper(ctx context.Context, beforePrompt func()) (bool, error) {
	client, external := helperClient()
	if res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus}); err == nil && res.Version == hostshelper.Version {
		return false, nil
	}
	if external {
		return false, errcode.Errorf(errcode.Service, "the helper at $"+EnvHostsHelperSocket+" does not answer")
	}
	// Tests must never install the system helper.
	if c.launchctl.Prefix != launchd.DefaultPrefix {
		return false, errcode.Errorf(errcode.Unsupported,
			"refusing to install the system helper with a test launchd prefix; set $"+EnvHostsHelperSocket)
	}

	src, err := helperSource(ctx)
	if err != nil {
		return false, err
	}
	plist, err := launchd.Plist(launchd.Agent{
		Label:   hostshelper.Label,
		Program: hostshelper.BinaryPath,
		Args: []string{
			"--uid", strconv.Itoa(os.Getuid()),
			"--label", hostshelper.Label,
			"--remove", hostshelper.BinaryPath,
			"--remove", hostshelper.PlistPath,
		},
		WorkDir:   "/",
		StdoutLog: "/dev/null",
		StderrLog: "/dev/null",
	})
	if err != nil {
		return false, err
	}
	stagedPlist := filepath.Join(c.Layout.Tmp, hostshelper.Label+".plist")
	if _, err := fsutil.WriteAtomic(stagedPlist, plist, 0o644); err != nil {
		return false, err
	}
	defer os.Remove(stagedPlist)

	q := func(p string) string { return "'" + p + "'" }
	for _, p := range []string{src, stagedPlist} {
		if err := paths.CheckSafe(p); err != nil || strings.Contains(p, "'") {
			return false, errcode.Newf(errcode.InvalidInput, "unsafe path %q", p)
		}
	}
	// The only commands run as root. install(1) sets owner and mode as it
	// copies; bootout replaces an older helper.
	cmd := strings.Join([]string{
		"/bin/mkdir -p /Library/PrivilegedHelperTools",
		"/usr/bin/install -o root -g wheel -m 0755 " + q(src) + " " + q(hostshelper.BinaryPath),
		"/usr/bin/install -o root -g wheel -m 0644 " + q(stagedPlist) + " " + q(hostshelper.PlistPath),
		"{ /bin/launchctl bootout system/" + hostshelper.Label + " 2>/dev/null; true; }",
		"/bin/launchctl bootstrap system " + q(hostshelper.PlistPath),
	}, " && ")
	if beforePrompt != nil {
		beforePrompt()
	}
	if err := resolver.Admin(ctx,
		"Covo wants to install its helper. It only manages Covo's lines in /etc/hosts (.local domains) and forwards ports 443 and 80 to Covo while it runs.", cmd); err != nil {
		return false, err
	}
	if _, err := fsutil.WriteAtomic(helperMarker(c.Layout), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return false, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		res, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus})
		if err == nil && res.Version == hostshelper.Version {
			return true, nil
		}
		if time.Now().After(deadline) {
			return false, errcode.Newf(errcode.Service, "the helper was installed but does not answer: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// helperSource finds covo-hostshelper next to the covo binary and checks it
// speaks the expected protocol version.
func helperSource(ctx context.Context) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locate covo binary: %w", err)
	}
	if exe, err = filepath.EvalSymlinks(exe); err != nil {
		return "", fmt.Errorf("locate covo binary: %w", err)
	}
	src := filepath.Join(filepath.Dir(exe), helperBinaryName)
	out, err := exec.CommandContext(ctx, src, "--version").Output()
	if err != nil {
		return "", errcode.New(errcode.NotFound, fmt.Errorf("%s not found or not runnable next to covo (build it with `go build ./cmd/covo-hostshelper`): %w", src, err))
	}
	if v := strings.TrimSpace(string(out)); v != hostshelper.Version {
		return "", errcode.Newf(errcode.Conflict, "%s speaks protocol %s, covo needs %s", src, v, hostshelper.Version)
	}
	return src, nil
}

// uninstallHelper asks the helper to clear Covo's hosts lines and remove
// itself. No password: the helper is already root.
func (c *Core) uninstallHelper(ctx context.Context) (bool, error) {
	return removeHelper(ctx, c.Layout)
}

func removeHelper(ctx context.Context, l paths.Layout) (bool, error) {
	client, external := helperClient()
	if external {
		// A test helper that no longer answers has already removed itself.
		if _, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpStatus}); err != nil {
			return false, nil
		}
	} else if _, err := os.Stat(helperMarker(l)); errors.Is(err, os.ErrNotExist) {
		return false, nil // installed by another Covo home, or not at all
	}
	if _, err := client.Do(ctx, hostshelper.Request{Op: hostshelper.OpUninstall}); err != nil {
		if !external {
			if _, statErr := os.Stat(hostshelper.PlistPath); errors.Is(statErr, os.ErrNotExist) {
				_ = os.Remove(helperMarker(l))
				return false, nil
			}
		}
		return false, errcode.New(errcode.Service, fmt.Errorf("remove Covo's helper: %w", err))
	}
	if !external {
		deadline := time.Now().Add(10 * time.Second)
		for {
			_, errBin := os.Stat(hostshelper.BinaryPath)
			_, errPlist := os.Stat(hostshelper.PlistPath)
			if errors.Is(errBin, os.ErrNotExist) && errors.Is(errPlist, os.ErrNotExist) {
				break
			}
			if time.Now().After(deadline) {
				return false, errcode.Errorf(errcode.Service, "the helper did not remove its files")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	if err := os.Remove(helperMarker(l)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return true, fmt.Errorf("remove helper marker: %w", err)
	}
	return true, nil
}
