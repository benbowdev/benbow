// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/php"
)

// The PHP compatibility check runs PHP_CodeSniffer with PHPCompatibility on
// a site's code and lists what would break on a newer PHP version, before
// switching. WordPress sites use PHPCompatibilityWP (which knows the
// polyfills WordPress ships) and, unless all is set, only wp-content: the
// WordPress core of a current release is compatible already.

// checkerPHP runs PHP_CodeSniffer (it needs PHP 7.2 or newer).
const checkerPHP = "8.4"

// PHPCheckMessage is one problem found in a file.
type PHPCheckMessage struct {
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Type    string `json:"type"` // "error" or "warning"
	Message string `json:"message"`
	Source  string `json:"source"`
}

// PHPCheckFile is a file with problems, relative to the site folder.
type PHPCheckFile struct {
	Path     string            `json:"path"`
	Messages []PHPCheckMessage `json:"messages"`
}

// PHPCheckResult is the outcome of CheckPHP.
type PHPCheckResult struct {
	Site      string `json:"site"`
	PHP       string `json:"php"`    // the site's current version
	Target    string `json:"target"` // the version checked against
	WordPress bool   `json:"wordpress"`
	// Scanned are the folders checked, relative to the site folder.
	Scanned  []string       `json:"scanned"`
	Errors   int            `json:"errors"`
	Warnings int            `json:"warnings"`
	Files    []PHPCheckFile `json:"files"`
	Seconds  float64        `json:"seconds"`
}

// isWordPress reports whether dir holds a WordPress installation.
func isWordPress(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "wp-includes", "version.php"))
	return err == nil
}

// CheckPHP checks a site's PHP code against target ("8.4"; default: the
// newest PHP version php.net fully supports). With all, a WordPress site's
// core files are checked too.
func (c *Core) CheckPHP(ctx context.Context, name, target string, all bool) (PHPCheckResult, error) {
	site, err := c.Store.Site(ctx, name)
	if err != nil {
		return PHPCheckResult{}, err
	}
	if target == "" {
		target = c.recommendedPHP()
	}
	if err := validMinor(target); err != nil {
		return PHPCheckResult{}, err
	}
	res := PHPCheckResult{Site: name, PHP: site.PHPMinor, Target: target, WordPress: isWordPress(site.Path), Files: []PHPCheckFile{}}
	paths := []string{site.Path}
	res.Scanned = []string{"."}
	if res.WordPress && !all {
		if _, err := os.Stat(filepath.Join(site.Path, "wp-content")); err == nil {
			paths = []string{filepath.Join(site.Path, "wp-content")}
			res.Scanned = []string{"wp-content"}
		}
	}

	cmd, err := c.phpcsCommand(ctx, res.WordPress, target, paths)
	if err != nil {
		return res, err
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	start := time.Now()
	runErr := cmd.Run()
	res.Seconds = time.Since(start).Round(100 * time.Millisecond).Seconds()
	// phpcs exits non-zero when it finds problems: the report decides.
	var report struct {
		Files map[string]struct {
			Messages []struct {
				Message  string `json:"message"`
				Source   string `json:"source"`
				Type     string `json:"type"`
				Line     int    `json:"line"`
				Column   int    `json:"column"`
				Severity int    `json:"severity"`
			} `json:"messages"`
		} `json:"files"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		msg := strings.TrimSpace(stderr.String() + "\n" + stdout.String())
		if len(msg) > 2000 {
			msg = msg[:2000] + "…"
		}
		return res, errcode.New(errcode.Service, fmt.Errorf("PHP_CodeSniffer failed (%w): %s", runErr, msg))
	}
	for path, f := range report.Files {
		if len(f.Messages) == 0 {
			continue
		}
		rel := relativeTo(site.Path, path)
		file := PHPCheckFile{Path: rel}
		for _, m := range f.Messages {
			t := strings.ToLower(m.Type)
			if t == "error" {
				res.Errors++
			} else {
				res.Warnings++
			}
			file.Messages = append(file.Messages, PHPCheckMessage{Line: m.Line, Column: m.Column, Type: t, Message: m.Message, Source: m.Source})
		}
		slices.SortFunc(file.Messages, func(a, b PHPCheckMessage) int { return cmp.Compare(a.Line, b.Line) })
		res.Files = append(res.Files, file)
	}
	slices.SortFunc(res.Files, func(a, b PHPCheckFile) int { return cmp.Compare(a.Path, b.Path) })
	return res, nil
}

// recommendedPHP is the newest PHP version Covo offers that php.net still
// fully supports.
func (c *Core) recommendedPHP() string {
	var best string
	for _, v := range c.Manifest.Versions("php", runtime.GOARCH) {
		m := minorOf(v)
		if php.SupportOf(m, time.Now()).Status != php.SupportActive {
			continue
		}
		if best == "" || compareMinor(m, best) > 0 {
			best = m
		}
	}
	return cmp.Or(best, checkerPHP)
}

func compareMinor(a, b string) int {
	as, bs := strings.SplitN(a, ".", 2), strings.SplitN(b, ".", 2)
	for i := range 2 {
		x, _ := strconv.Atoi(as[min(i, len(as)-1)])
		y, _ := strconv.Atoi(bs[min(i, len(bs)-1)])
		if x != y {
			return cmp.Compare(x, y)
		}
	}
	return 0
}

// phpcsCommand installs PHP_CodeSniffer and the PHPCompatibility standards
// (pinned in the manifest) and prepares the check.
func (c *Core) phpcsCommand(ctx context.Context, wordpress bool, target string, paths []string) (*exec.Cmd, error) {
	v, err := c.ensurePHP(ctx, checkerPHP)
	if err != nil {
		return nil, err
	}
	bin, err := c.ensurePHPCLI(ctx, v)
	if err != nil {
		return nil, err
	}
	dirs := map[string]string{}
	for _, comp := range []string{"phpcs", "phpcompat", "phpcsutils", "phpcompat-paragonie", "phpcompat-wp"} {
		art, err := c.Manifest.Resolve(comp, "")
		if err != nil {
			return nil, err
		}
		inst, err := c.Installer.Install(ctx, art)
		if err != nil {
			return nil, err
		}
		dirs[comp] = inst.Dir
	}
	standard := "PHPCompatibility"
	if wordpress {
		standard = "PHPCompatibilityWP"
	}
	installed := strings.Join([]string{dirs["phpcompat"], dirs["phpcsutils"], dirs["phpcompat-paragonie"], dirs["phpcompat-wp"]}, ",")
	args := []string{
		"-n", "-d", "memory_limit=-1", "-d", "error_reporting=E_ALL&~E_DEPRECATED",
		filepath.Join(dirs["phpcs"], "phpcs.phar"),
		"--standard=" + standard,
		"--runtime-set", "testVersion", target,
		"--runtime-set", "installed_paths", installed,
		"--report=json", "-q", "--no-cache", "--no-colors",
		"--extensions=php,inc,phtml",
		"--ignore=*/node_modules/*,*/.git/*,*/wp-content/uploads/*,*/wp-content/cache/*",
		"--parallel=" + strconv.Itoa(max(1, runtime.NumCPU())),
	}
	args = append(args, paths...)
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = c.Layout.Tmp
	cmd.Env = append(os.Environ(), "PHP_INI_SCAN_DIR=")
	return cmd, nil
}

// relativeTo makes path relative to base, following symbolic links in both
// (/var is /private/var on macOS); path itself if it is not inside base.
func relativeTo(base, path string) string {
	for _, b := range []string{base, resolve(base)} {
		for _, p := range []string{path, resolve(path)} {
			if rel, err := filepath.Rel(b, p); err == nil && !strings.HasPrefix(rel, "..") {
				return rel
			}
		}
	}
	return path
}
