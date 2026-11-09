// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/php"
	"github.com/benbowdev/benbow/internal/store"
)

var minorRe = regexp.MustCompile(`^\d+\.\d+$`)

// PHPInstall is the result of installing a PHP version.
type PHPInstall struct {
	store.PHPVersion
	Binary  string   `json:"binary"`
	Fresh   bool     `json:"fresh"`
	Modules []string `json:"modules"`
}

// InstallPHP installs the newest pinned build matching spec ("8.4" or
// "8.4.23") and records it. Installing an installed version is a no-op.
func (c *Core) InstallPHP(ctx context.Context, spec string) (PHPInstall, error) {
	var res PHPInstall
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.installPHP(ctx, spec)
		return err
	})
	return res, err
}

func (c *Core) installPHP(ctx context.Context, spec string) (PHPInstall, error) {
	art, err := c.Manifest.Resolve("php", spec)
	if err != nil {
		return PHPInstall{}, err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return PHPInstall{}, err
	}
	bin := filepath.Join(inst.Dir, "php-fpm")

	// Prove the binary actually runs on this Mac and is what the manifest says.
	out, err := runQuiet(ctx, bin, "-n", "-v")
	if err != nil {
		return PHPInstall{}, errcode.New(errcode.Service, fmt.Errorf("run %s -v: %w", bin, err))
	}
	if !strings.HasPrefix(out, "PHP "+art.Version+" ") {
		return PHPInstall{}, errcode.Newf(errcode.Checksum,
			"php-fpm reports %q, expected PHP %s", firstLine(out), art.Version)

	}
	mods, err := runQuiet(ctx, bin, "-n", "-m")
	if err != nil {
		return PHPInstall{}, errcode.New(errcode.Service, fmt.Errorf("run %s -m: %w", bin, err))
	}

	v := store.PHPVersion{
		Version:     art.Version,
		Minor:       minorOf(art.Version),
		Path:        inst.Dir,
		SHA256:      art.SHA256,
		InstalledAt: time.Now(),
	}
	if err := c.Store.PutPHP(ctx, v); err != nil {
		return PHPInstall{}, err
	}
	return PHPInstall{PHPVersion: v, Binary: bin, Fresh: inst.Fresh, Modules: parseModules(mods)}, nil
}

// ensurePHP returns the installed version for minor, installing it if needed.
func (c *Core) ensurePHP(ctx context.Context, minor string) (store.PHPVersion, error) {
	v, err := c.Store.LatestPHP(ctx, minor)
	if err == nil {
		return v, nil
	}
	if errcode.Code(err) != errcode.NotFound {
		return store.PHPVersion{}, err
	}
	inst, err := c.installPHP(ctx, minor)
	if err != nil {
		return store.PHPVersion{}, err
	}
	return inst.PHPVersion, nil
}

// PHPList is the installed and available PHP versions.
type PHPList struct {
	Installed []store.PHPVersion `json:"installed"`
	Available []string           `json:"available"`
	// Support tells, per minor version (installed or available), whether
	// php.net still fixes it.
	Support []php.Support `json:"support"`
}

// ListPHP returns installed versions and the versions Benbow can install.
func (c *Core) ListPHP(ctx context.Context) (PHPList, error) {
	inst, err := c.Store.ListPHP(ctx)
	if err != nil {
		return PHPList{}, err
	}
	if inst == nil {
		inst = []store.PHPVersion{}
	}
	list := PHPList{Installed: inst, Available: c.Manifest.Versions("php", runtime.GOARCH), Support: []php.Support{}}
	seen := map[string]bool{}
	for _, v := range append(list.Available, minorsOf(inst)...) {
		if m := minorOf(v); !seen[m] {
			seen[m] = true
			list.Support = append(list.Support, php.SupportOf(m, time.Now()))
		}
	}
	slices.SortFunc(list.Support, func(a, b php.Support) int { return -store.CompareVersions(a.Minor, b.Minor) })
	return list, nil
}

func minorsOf(vs []store.PHPVersion) []string {
	out := make([]string, 0, len(vs))
	for _, v := range vs {
		out = append(out, v.Minor)
	}
	return out
}

func validMinor(v string) error {
	if !minorRe.MatchString(v) {
		return errcode.Newf(errcode.InvalidInput, "PHP version must look like 8.4, got %q", v)
	}
	return nil
}

func minorOf(version string) string {
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return version
	}
	return parts[0] + "." + parts[1]
}

func parseModules(out string) []string {
	mods := []string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "[") {
			continue
		}
		mods = append(mods, line)
	}
	return mods
}

func runQuiet(ctx context.Context, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var out bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%w: %s", err, strings.TrimSpace(out.String()))
	}
	return out.String(), nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
