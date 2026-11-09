// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

// Blueprints are WordPress site templates: WordPress version and language,
// themes, plugins, options, wp-config constants and must-use plugins.
// Themes and plugins come from wordpress.org, a zip URL, a GitHub
// repository or (when the system has it) git. Benbow downloads them itself,
// records the SHA-256 the first time and refuses a source whose content
// changed since, unless the user accepts the change.

import (
	"archive/zip"
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/benbowdev/benbow/internal/version"
)

// SnapshotPreBlueprint is taken before a blueprint is applied to an
// existing site.
const SnapshotPreBlueprint = "pre-blueprint"

// BlueprintNotice is shown before a blueprint installs anything.
const BlueprintNotice = "Blueprints install third-party code (themes and plugins) into your site. " +
	"Use only sources you trust. Benbow records each download's SHA-256 the first time and stops if it changes."

// Where blueprint sources come from; variables so tests can point them at
// a local server.
var (
	wporgAPI       = "https://api.wordpress.org"
	wporgDownloads = "https://downloads.wordpress.org"
	githubAPI      = "https://api.github.com"
	gitBinary      = "/usr/bin/git"
)

// Blueprint is a WordPress site template (its JSON form).
type Blueprint struct {
	Name        string              `json:"name"`
	Description string              `json:"description,omitempty"`
	WordPress   BlueprintWordPress  `json:"wordpress,omitzero"`
	Themes      []BlueprintPackage  `json:"themes,omitempty"`
	Plugins     []BlueprintPackage  `json:"plugins,omitempty"`
	Options     BlueprintOptions    `json:"options,omitzero"`
	Constants   map[string]any      `json:"constants,omitempty"`
	MUPlugins   []BlueprintMUPlugin `json:"mu_plugins,omitempty"`
}

// BlueprintWordPress picks the WordPress version ("": Benbow's) and language
// ("": English).
type BlueprintWordPress struct {
	Version string `json:"version,omitempty"`
	Locale  string `json:"locale,omitempty"`
}

// BlueprintPackage is a theme or plugin. Exactly one source: a
// wordpress.org slug (with an optional version), url (a zip), github
// (owner/repo, optional ref) or git (a clone URL, optional ref).
type BlueprintPackage struct {
	Slug    string `json:"slug,omitempty"`
	Version string `json:"version,omitempty"`
	URL     string `json:"url,omitempty"`
	GitHub  string `json:"github,omitempty"`
	Git     string `json:"git,omitempty"`
	Ref     string `json:"ref,omitempty"`
	// Activate defaults to true for plugins and false for themes.
	Activate *bool `json:"activate,omitempty"`
}

// BlueprintOptions are WordPress settings.
type BlueprintOptions struct {
	Permalinks          string            `json:"permalinks,omitempty"`
	Timezone            string            `json:"timezone,omitempty"`
	RemoveSampleContent bool              `json:"remove_sample_content,omitempty"`
	Set                 map[string]string `json:"set,omitempty"`
}

// BlueprintMUPlugin is a must-use plugin file.
type BlueprintMUPlugin struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

// BlueprintInfo is a saved blueprint in lists.
type BlueprintInfo struct {
	store.Blueprint
	Description string `json:"description,omitempty"`
	Themes      int    `json:"themes"`
	Plugins     int    `json:"plugins"`
}

// BlueprintResult tells what applying a blueprint did.
type BlueprintResult struct {
	Blueprint string                  `json:"blueprint"`
	Installed []string                `json:"installed"`
	Sources   []store.BlueprintSource `json:"sources"`
	Warnings  []string                `json:"warnings,omitempty"`
	Snapshot  string                  `json:"snapshot,omitempty"`
	Notice    string                  `json:"notice"`
}

var (
	blueprintNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	bpSlugRe        = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,99}$`)
	bpVersionRe     = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+-]{0,39}$`)
	bpGitHubRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9._-]{1,100}$`)
	bpRefRe         = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._/-]{0,199}$`)
	bpLocaleRe      = regexp.MustCompile(`^[a-z]{2,3}(_[A-Z]{2})?(_[a-z0-9]+)?$`)
	bpTimezoneRe    = regexp.MustCompile(`^(UTC|[A-Za-z_]+(/[A-Za-z0-9_+-]+){1,2})$`)
	bpConstantRe    = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,63}$`)
	bpOptionRe      = regexp.MustCompile(`^[a-z0-9_]{1,64}$`)
	bpMUNameRe      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}\.php$`)
	commitRe        = regexp.MustCompile(`^[0-9a-f]{40}$`)
	bpGitURLRe      = regexp.MustCompile(`^(https://[A-Za-z0-9.-]+(:[0-9]+)?/[A-Za-z0-9._~/-]+|git@[A-Za-z0-9.-]+:[A-Za-z0-9._~/-]+)$`)
)

// Constants a blueprint may not set: the database and the secret keys
// belong to each site.
var bpReservedConstants = []string{
	"DB_NAME", "DB_USER", "DB_PASSWORD", "DB_HOST", "DB_CHARSET", "DB_COLLATE",
	"AUTH_KEY", "SECURE_AUTH_KEY", "LOGGED_IN_KEY", "NONCE_KEY",
	"AUTH_SALT", "SECURE_AUTH_SALT", "LOGGED_IN_SALT", "NONCE_SALT", "ABSPATH",
}

// Options a blueprint may not set: they are the site's own.
var bpReservedOptions = []string{"siteurl", "home", "admin_email", "active_plugins", "template", "stylesheet"}

const (
	maxBlueprintSize = 2 << 20   // JSON
	maxMUPluginSize  = 256 << 10 // per file
	maxPackageZip    = 200 << 20 // download
	maxPackageFiles  = 20000
	maxPackageTotal  = 500 << 20 // extracted
)

// ParseBlueprint reads and checks a blueprint's JSON.
func ParseBlueprint(data []byte) (Blueprint, error) {
	var bp Blueprint
	if len(data) > maxBlueprintSize {
		return bp, errcode.Errorf(errcode.InvalidInput, "blueprint is larger than 2 MiB")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&bp); err != nil {
		return bp, errcode.Errorf(errcode.InvalidInput, "blueprint is not valid JSON: "+err.Error())
	}
	return bp, bp.Validate()
}

// Validate checks every field, so nothing odd reaches wp-cli or a URL.
func (bp Blueprint) Validate() error {
	bad := func(msg string) error { return errcode.Errorf(errcode.InvalidInput, "blueprint: "+msg) }
	if !blueprintNameRe.MatchString(bp.Name) {
		return bad(fmt.Sprintf("name %q must be lowercase letters, digits and dashes", bp.Name))
	}
	if v := bp.WordPress.Version; v != "" && !bpVersionRe.MatchString(v) {
		return bad(fmt.Sprintf("WordPress version %q is not valid", v))
	}
	if l := bp.WordPress.Locale; l != "" && !bpLocaleRe.MatchString(l) {
		return bad(fmt.Sprintf("locale %q is not valid (e.g. it_IT)", l))
	}
	for kind, list := range map[string][]BlueprintPackage{"theme": bp.Themes, "plugin": bp.Plugins} {
		for _, p := range list {
			if err := p.validate(); err != nil {
				return bad(kind + " " + p.label() + ": " + err.Error())
			}
		}
	}
	active := 0
	for _, t := range bp.Themes {
		if t.Activate != nil && *t.Activate {
			active++
		}
	}
	if active > 1 {
		return bad("only one theme can be activated")
	}
	if p := bp.Options.Permalinks; p != "" && (!strings.HasPrefix(p, "/") || strings.ContainsAny(p, " \n\"'\\")) {
		return bad(fmt.Sprintf("permalinks %q must start with / (e.g. /%%postname%%/)", p))
	}
	if tz := bp.Options.Timezone; tz != "" && !bpTimezoneRe.MatchString(tz) {
		return bad(fmt.Sprintf("timezone %q is not valid (e.g. Europe/Rome)", tz))
	}
	for k, v := range bp.Options.Set {
		if !bpOptionRe.MatchString(k) || slices.Contains(bpReservedOptions, k) {
			return bad(fmt.Sprintf("option %q cannot be set by a blueprint", k))
		}
		if len(v) > 64<<10 {
			return bad(fmt.Sprintf("option %q is too long", k))
		}
	}
	for k, v := range bp.Constants {
		if !bpConstantRe.MatchString(k) || slices.Contains(bpReservedConstants, k) {
			return bad(fmt.Sprintf("constant %q cannot be set by a blueprint", k))
		}
		switch v := v.(type) {
		case bool, float64:
		case string:
			if strings.ContainsAny(v, "\n\r") || len(v) > 4096 {
				return bad(fmt.Sprintf("constant %s: value must be one line", k))
			}
		default:
			return bad(fmt.Sprintf("constant %s: value must be text, a number or true/false", k))
		}
	}
	seen := map[string]bool{}
	for _, m := range bp.MUPlugins {
		if !bpMUNameRe.MatchString(m.Name) || seen[m.Name] {
			return bad(fmt.Sprintf("must-use plugin name %q must be a unique file name ending in .php", m.Name))
		}
		seen[m.Name] = true
		if len(m.Content) > maxMUPluginSize || !strings.HasPrefix(strings.TrimSpace(m.Content), "<?php") {
			return bad(fmt.Sprintf("must-use plugin %s must start with <?php and be under 256 KiB", m.Name))
		}
	}
	return nil
}

func (p BlueprintPackage) label() string {
	return cmp.Or(p.Slug, p.GitHub, p.Git, p.URL)
}

func (p BlueprintPackage) validate() error {
	sources := 0
	for _, s := range []string{p.URL, p.GitHub, p.Git} {
		if s != "" {
			sources++
		}
	}
	switch {
	case sources > 1:
		return errors.New("use only one of url, github and git")
	case sources == 0 && p.Slug == "":
		return errors.New("needs a slug (wordpress.org), url, github or git")
	case p.Slug != "" && !bpSlugRe.MatchString(p.Slug):
		return fmt.Errorf("slug %q must be lowercase letters, digits, dots, dashes and underscores", p.Slug)
	case p.Version != "" && (sources > 0 || !bpVersionRe.MatchString(p.Version)):
		return errors.New("version is only for wordpress.org packages, like 1.2.3")
	case p.Ref != "" && (p.GitHub == "" && p.Git == "" || !bpRefRe.MatchString(p.Ref) || strings.Contains(p.Ref, "..")):
		return errors.New("ref (branch, tag or commit) is only for github and git, and must be a plain name")
	case p.GitHub != "" && !bpGitHubRe.MatchString(p.GitHub):
		return fmt.Errorf("github %q must be owner/repository", p.GitHub)
	case p.Git != "" && !bpGitURLRe.MatchString(p.Git):
		return fmt.Errorf("git %q must be an https:// or git@ URL", p.Git)
	}
	if p.URL != "" {
		u, err := url.Parse(p.URL)
		if err != nil || u.Host == "" || u.User != nil {
			return fmt.Errorf("url %q is not valid", p.URL)
		}
		if u.Scheme != "https" && (u.Scheme != "http" || !isLoopbackHost(u.Hostname())) {
			return fmt.Errorf("url %q must use https", p.URL)
		}
	}
	return nil
}

func isLoopbackHost(h string) bool {
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// --- saved blueprints ---

// SaveBlueprint checks and stores a blueprint (replacing one with the same
// name). Overwrite false refuses an existing name.
func (c *Core) SaveBlueprint(ctx context.Context, bp Blueprint, overwrite bool) (BlueprintInfo, error) {
	if err := bp.Validate(); err != nil {
		return BlueprintInfo{}, err
	}
	if _, err := c.Store.Blueprint(ctx, bp.Name); err == nil && !overwrite {
		return BlueprintInfo{}, errcode.Newf(errcode.Conflict, "blueprint %q already exists", bp.Name)
	}
	data, err := json.MarshalIndent(bp, "", "  ")
	if err != nil {
		return BlueprintInfo{}, fmt.Errorf("encode blueprint: %w", err)
	}
	if err := c.Store.SaveBlueprint(ctx, bp.Name, string(data)); err != nil {
		return BlueprintInfo{}, err
	}
	saved, err := c.Store.Blueprint(ctx, bp.Name)
	if err != nil {
		return BlueprintInfo{}, err
	}
	return blueprintInfo(saved, bp), nil
}

func blueprintInfo(s store.Blueprint, bp Blueprint) BlueprintInfo {
	return BlueprintInfo{Blueprint: s, Description: bp.Description, Themes: len(bp.Themes), Plugins: len(bp.Plugins)}
}

// GetBlueprint returns a saved blueprint.
func (c *Core) GetBlueprint(ctx context.Context, name string) (Blueprint, error) {
	s, err := c.Store.Blueprint(ctx, name)
	if err != nil {
		return Blueprint{}, err
	}
	var bp Blueprint
	if err := json.Unmarshal([]byte(s.Data), &bp); err != nil {
		return bp, fmt.Errorf("read blueprint %s: %w", name, err)
	}
	return bp, nil
}

// ListBlueprints lists saved blueprints.
func (c *Core) ListBlueprints(ctx context.Context) ([]BlueprintInfo, error) {
	list, err := c.Store.Blueprints(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]BlueprintInfo, 0, len(list))
	for _, s := range list {
		var bp Blueprint
		_ = json.Unmarshal([]byte(s.Data), &bp)
		out = append(out, blueprintInfo(s, bp))
	}
	return out, nil
}

// DeleteBlueprint removes a saved blueprint (recorded checksums stay).
func (c *Core) DeleteBlueprint(ctx context.Context, name string) error {
	return c.Store.DeleteBlueprint(ctx, name)
}

// BlueprintSources lists every download Benbow recorded.
func (c *Core) BlueprintSources(ctx context.Context) ([]store.BlueprintSource, error) {
	return c.Store.BlueprintSources(ctx)
}

// --- GitHub token ---

func (c *Core) githubKeychain() (keychain.Item, error) {
	kc, err := keychain.Login(c.launchctl.Prefix+".github", "github")
	kc.Label = "Benbow GitHub token"
	return kc, err
}

// SetGitHubToken stores the token used for private GitHub repositories in
// the login keychain ("" deletes it).
func (c *Core) SetGitHubToken(ctx context.Context, token string) error {
	kc, err := c.githubKeychain()
	if err != nil {
		return err
	}
	token = strings.TrimSpace(token)
	if token == "" {
		_, err = kc.Delete(ctx)
		return err
	}
	if strings.ContainsAny(token, " \n\t") {
		return errcode.Errorf(errcode.InvalidInput, "the token must be one word")
	}
	return kc.Set(ctx, token)
}

// HasGitHubToken reports whether a GitHub token is stored.
func (c *Core) HasGitHubToken(ctx context.Context) bool {
	kc, err := c.githubKeychain()
	if err != nil {
		return false
	}
	ok, _ := kc.Exists(ctx)
	return ok
}

func (c *Core) githubToken(ctx context.Context) string {
	kc, err := c.githubKeychain()
	if err != nil {
		return ""
	}
	t, _ := kc.Get(ctx)
	return t
}

// --- applying ---

// ApplyOptions tune ApplyBlueprint.
type ApplyOptions struct {
	// AcceptChanges records a new SHA-256 for sources whose content changed
	// since Benbow first saw them, instead of stopping.
	AcceptChanges bool
	DryRun        bool
}

// isWordPressDir reports whether dir holds a WordPress installation.
func isWordPressDir(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "wp-includes", "version.php"))
	return err == nil
}

// ApplyBlueprint applies a saved blueprint to an existing WordPress site,
// after a snapshot. Themes, plugins and must-use plugins already there are
// left alone.
func (c *Core) ApplyBlueprint(ctx context.Context, siteName, name string, opts ApplyOptions) (BlueprintResult, error) {
	var res BlueprintResult
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.applySavedBlueprint(ctx, siteName, name, opts)
		return err
	})
	return res, err
}

func (c *Core) applySavedBlueprint(ctx context.Context, siteName, name string, opts ApplyOptions) (BlueprintResult, error) {
	bp, err := c.GetBlueprint(ctx, name)
	if err != nil {
		return BlueprintResult{}, err
	}
	site, err := c.Store.Site(ctx, siteName)
	if err != nil {
		return BlueprintResult{}, err
	}
	if !isWordPressDir(site.Path) {
		return BlueprintResult{}, errcode.Newf(errcode.InvalidInput, "site %q is not a WordPress site", siteName)
	}
	if opts.DryRun {
		return c.planBlueprint(site, bp), nil
	}
	snap, err := c.createSnapshot(ctx, siteName, SnapshotPreBlueprint, "before blueprint "+name, "")
	if err != nil {
		return BlueprintResult{}, fmt.Errorf("the snapshot before the blueprint failed, nothing was changed: %w", err)
	}
	res, err := c.applyBlueprint(ctx, site, bp, opts)
	res.Snapshot = snap.ID
	if err != nil {
		return res, fmt.Errorf("%w (snapshot %s has the site as it was: benbow snapshot restore %s %s)", err, snap.ID, siteName, snap.ID)
	}
	return res, nil
}

// planBlueprint says what a blueprint would install, without downloading.
func (c *Core) planBlueprint(site store.Site, bp Blueprint) BlueprintResult {
	res := BlueprintResult{Blueprint: bp.Name, Installed: []string{}, Sources: []store.BlueprintSource{}, Notice: BlueprintNotice}
	wpc := filepath.Join(site.Path, "wp-content")
	for _, kind := range []string{"theme", "plugin"} {
		list := bp.Themes
		if kind == "plugin" {
			list = bp.Plugins
		}
		for _, p := range list {
			if slug := p.folder(); slug != "" && exists(filepath.Join(wpc, kind+"s", slug)) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s is already installed: left as it is", kind, slug))
				continue
			}
			res.Installed = append(res.Installed, kind+" "+p.label())
		}
	}
	for _, m := range bp.MUPlugins {
		res.Installed = append(res.Installed, "must-use plugin "+m.Name)
	}
	return res
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// folder is the package's folder name in wp-content when it is known
// before downloading.
func (p BlueprintPackage) folder() string {
	switch {
	case p.Slug != "":
		return p.Slug
	case p.GitHub != "":
		return strings.ToLower(path.Base(p.GitHub))
	case p.Git != "":
		return strings.ToLower(strings.TrimSuffix(path.Base(strings.ReplaceAll(p.Git, ":", "/")), ".git"))
	}
	return ""
}

func (c *Core) applyBlueprint(ctx context.Context, site store.Site, bp Blueprint, opts ApplyOptions) (BlueprintResult, error) {
	res := BlueprintResult{Blueprint: bp.Name, Installed: []string{}, Sources: []store.BlueprintSource{}, Notice: BlueprintNotice}
	wp := func(args ...string) error { return c.wp(ctx, site, "", args...) }
	if v := bp.WordPress.Version; v != "" {
		cur, _ := c.wpOutput(ctx, site, "core", "version")
		if cur != v {
			if err := wp("core", "update", "--version="+v, "--force"); err != nil {
				return res, err
			}
			if err := wp("core", "update-db"); err != nil {
				return res, err
			}
			res.Installed = append(res.Installed, "WordPress "+v)
		}
	}
	if l := bp.WordPress.Locale; l != "" && l != "en_US" {
		if err := wp("language", "core", "install", l, "--activate"); err != nil {
			return res, err
		}
		res.Installed = append(res.Installed, "language "+l)
	}
	wpc := filepath.Join(site.Path, "wp-content")
	for _, kind := range []string{"theme", "plugin"} {
		list := bp.Themes
		if kind == "plugin" {
			list = bp.Plugins
		}
		for _, p := range list {
			slug, src, err := c.installPackage(ctx, kind, p, filepath.Join(wpc, kind+"s"), opts.AcceptChanges, &res)
			if err != nil {
				return res, err
			}
			if src != nil {
				res.Sources = append(res.Sources, *src)
			}
			activate := kind == "plugin"
			if p.Activate != nil {
				activate = *p.Activate
			}
			if activate {
				if err := wp(kind, "activate", slug); err != nil {
					return res, err
				}
			}
		}
	}
	if len(bp.MUPlugins) > 0 {
		dir := filepath.Join(wpc, "mu-plugins")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return res, fmt.Errorf("create %s: %w", dir, err)
		}
		for _, m := range bp.MUPlugins {
			p := filepath.Join(dir, m.Name)
			if exists(p) {
				res.Warnings = append(res.Warnings, fmt.Sprintf("must-use plugin %s is already there: left as it is", m.Name))
				continue
			}
			if err := os.WriteFile(p, []byte(m.Content), 0o644); err != nil { //nolint:gosec // PHP the web server reads
				return res, fmt.Errorf("write %s: %w", p, err)
			}
			res.Installed = append(res.Installed, "must-use plugin "+m.Name)
		}
	}
	names := make([]string, 0, len(bp.Constants))
	for k := range bp.Constants {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		args := []string{"config", "set", k}
		switch v := bp.Constants[k].(type) {
		case bool:
			args = append(args, strconv.FormatBool(v), "--raw")
		case float64:
			args = append(args, strconv.FormatFloat(v, 'f', -1, 64), "--raw")
		case string:
			args = append(args, v)
		}
		if err := wp(append(args, "--type=constant")...); err != nil {
			return res, err
		}
	}
	o := bp.Options
	if o.Permalinks != "" {
		if err := wp("rewrite", "structure", o.Permalinks); err != nil {
			return res, err
		}
	}
	if o.Timezone != "" {
		if err := wp("option", "update", "timezone_string", o.Timezone); err != nil {
			return res, err
		}
	}
	keys := make([]string, 0, len(o.Set))
	for k := range o.Set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if err := wp("option", "update", k, o.Set[k]); err != nil {
			return res, err
		}
	}
	if o.RemoveSampleContent {
		if err := c.removeSampleContent(ctx, site); err != nil {
			return res, err
		}
		res.Installed = append(res.Installed, "sample content removed")
	}
	return res, nil
}

// wpOutput runs wp-cli and returns its trimmed output.
func (c *Core) wpOutput(ctx context.Context, site store.Site, args ...string) (string, error) {
	cmd, err := c.wpCommand(ctx, site, args...)
	if err != nil {
		return "", err
	}
	var out, errs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "", errcode.New(errcode.Service, fmt.Errorf("wp %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(errs.String())))
	}
	return strings.TrimSpace(out.String()), nil
}

// removeSampleContent deletes WordPress's own sample post, page, draft
// privacy page and the Hello Dolly and Akismet plugins, when they are
// still there.
func (c *Core) removeSampleContent(ctx context.Context, site store.Site) error {
	out, err := c.wpOutput(ctx, site, "post", "list", "--post_type=post,page", "--post_status=any", "--format=json", "--fields=ID,post_name,post_status")
	if err != nil {
		return err
	}
	var posts []struct {
		ID     int    `json:"ID"`
		Name   string `json:"post_name"`
		Status string `json:"post_status"`
	}
	if err := json.Unmarshal([]byte(out), &posts); err != nil {
		return fmt.Errorf("read posts: %w", err)
	}
	var ids []string
	for _, p := range posts {
		if p.Name == "hello-world" || p.Name == "sample-page" || (p.Name == "privacy-policy" && p.Status == "draft") {
			ids = append(ids, strconv.Itoa(p.ID))
		}
	}
	if len(ids) > 0 {
		if err := c.wp(ctx, site, "", append([]string{"post", "delete"}, append(ids, "--force")...)...); err != nil {
			return err
		}
	}
	for _, p := range []string{"hello", "akismet"} {
		if exists(filepath.Join(site.Path, "wp-content", "plugins", p)) || exists(filepath.Join(site.Path, "wp-content", "plugins", p+".php")) {
			if err := c.wp(ctx, site, "", "plugin", "delete", p); err != nil {
				return err
			}
		}
	}
	return nil
}

// installPackage downloads (or clones) a theme or plugin, checks it
// against the recorded SHA-256 and moves it into dir. It returns the
// folder name. A package already in dir is left alone (src nil).
func (c *Core) installPackage(ctx context.Context, kind string, p BlueprintPackage, dir string, accept bool, res *BlueprintResult) (slug string, src *store.BlueprintSource, err error) {
	if f := p.folder(); f != "" && exists(filepath.Join(dir, f)) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s is already installed: left as it is", kind, f))
		return f, nil, nil
	}
	if err := os.MkdirAll(c.Layout.Tmp, 0o700); err != nil {
		return "", nil, fmt.Errorf("create tmp: %w", err)
	}
	work, err := os.MkdirTemp(c.Layout.Tmp, "blueprint-")
	if err != nil {
		return "", nil, fmt.Errorf("create tmp: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	var key, sum, root string
	switch {
	case p.Git != "":
		key, sum, root, err = c.clonePackage(ctx, p, work)
	default:
		var dl, auth string
		dl, key, auth, err = c.packageURL(ctx, kind, p)
		if err != nil {
			return "", nil, err
		}
		var zipPath string
		zipPath, sum, err = c.downloadZip(ctx, dl, auth, work)
		if err != nil {
			return "", nil, err
		}
		root, err = unzipPackage(zipPath, filepath.Join(work, "x"))
	}
	if err != nil {
		return "", nil, err
	}
	rec, seen, err := c.Store.BlueprintSourceFor(ctx, key)
	if err != nil {
		return "", nil, err
	}
	if seen && !strings.EqualFold(rec.SHA256, sum) && !accept {
		return "", nil, errcode.Newf(errcode.Checksum,
			"%s %s changed since Benbow first used it on %s (SHA-256 was %s, now %s). If you trust the new content, apply again accepting changes",
			kind, p.label(), rec.FirstUsed.Local().Format("2006-01-02"), rec.SHA256, sum)

	}
	if seen && !strings.EqualFold(rec.SHA256, sum) {
		res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s changed since first use: new SHA-256 accepted", kind, p.label()))
	}
	slug = p.folder()
	if slug == "" {
		slug = strings.ToLower(filepath.Base(root))
		if !bpSlugRe.MatchString(slug) {
			return "", nil, errcode.Newf(errcode.InvalidInput, "%s %s: cannot tell its folder name; add a slug", kind, p.label())
		}
		if exists(filepath.Join(dir, slug)) {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s %s is already installed: left as it is", kind, slug))
			return slug, nil, nil
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create %s: %w", dir, err)
	}
	if err := os.Rename(root, filepath.Join(dir, slug)); err != nil {
		return "", nil, fmt.Errorf("install %s %s: %w", kind, slug, err)
	}
	if err := c.Store.RecordBlueprintSource(ctx, key, sum); err != nil {
		return "", nil, err
	}
	rec, _, _ = c.Store.BlueprintSourceFor(ctx, key)
	res.Installed = append(res.Installed, kind+" "+slug)
	return slug, &rec, nil
}

// packageURL resolves where to download a package: wordpress.org packages
// are pinned to a version (the latest when none is given), GitHub refs to
// a commit. key is what the SHA-256 is recorded under.
func (c *Core) packageURL(ctx context.Context, kind string, p BlueprintPackage) (dl, key, auth string, err error) {
	switch {
	case p.URL != "":
		return p.URL, p.URL, "", nil
	case p.GitHub != "":
		token := c.githubToken(ctx)
		if token != "" {
			auth = "Bearer " + token
		}
		ref := cmp.Or(p.Ref, "HEAD")
		body, err := c.httpGet(ctx, githubAPI+"/repos/"+p.GitHub+"/commits/"+url.PathEscape(ref), auth, "application/vnd.github.sha", 1<<10)
		if err != nil {
			if token == "" {
				err = fmt.Errorf("%w (for a private repository, store a token: benbow blueprint github-token)", err)
			}
			return "", "", "", err
		}
		sha := strings.TrimSpace(string(body))
		if !commitRe.MatchString(sha) {
			return "", "", "", errcode.Errorf(errcode.Download, "GitHub did not return a commit for "+p.GitHub+"@"+ref)
		}
		return githubAPI + "/repos/" + p.GitHub + "/zipball/" + sha, "https://github.com/" + p.GitHub + "@" + sha, auth, nil
	}
	version := p.Version
	if version == "" {
		q := url.Values{"action": {kind + "_information"}, "request[slug]": {p.Slug}}
		body, err := c.httpGet(ctx, wporgAPI+"/"+kind+"s/info/1.2/?"+q.Encode(), "", "application/json", 4<<20)
		if err != nil {
			return "", "", "", err
		}
		var info struct {
			Version string `json:"version"`
			Error   string `json:"error"`
		}
		if json.Unmarshal(body, &info) != nil || info.Version == "" || !bpVersionRe.MatchString(info.Version) {
			return "", "", "", errcode.Newf(errcode.NotFound, "%s %q not found on wordpress.org", kind, p.Slug)
		}
		version = info.Version
	}
	dl = fmt.Sprintf("%s/%s/%s.%s.zip", wporgDownloads, kind, p.Slug, version)
	return dl, dl, "", nil
}

// httpGet fetches a small response.
func (c *Core) httpGet(ctx context.Context, u, auth, accept string, limit int64) ([]byte, error) {
	resp, err := c.httpDo(ctx, u, auth, accept)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, errcode.New(errcode.Download, fmt.Errorf("read %s: %w", u, err))
	}
	return body, nil
}

func (c *Core) httpDo(ctx context.Context, u, auth, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, errcode.New(errcode.Download, fmt.Errorf("download %s: %w", u, err))
	}
	req.Header.Set("User-Agent", version.UserAgent())
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	client := c.Installer.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute, Transport: version.Transport{}}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errcode.New(errcode.Download, fmt.Errorf("download %s: %w", u, err))
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, errcode.New(errcode.Download, fmt.Errorf("download %s: HTTP %d", u, resp.StatusCode))
	}
	return resp, nil
}

// downloadZip saves u into dir and returns its path and SHA-256.
func (c *Core) downloadZip(ctx context.Context, u, auth, dir string) (file, sum string, err error) {
	resp, err := c.httpDo(ctx, u, auth, "")
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	file = filepath.Join(dir, "package.zip")
	f, err := os.Create(file)
	if err != nil {
		return "", "", fmt.Errorf("create %s: %w", file, err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxPackageZip+1))
	if err != nil {
		return "", "", errcode.New(errcode.Download, fmt.Errorf("download %s: %w", u, err))
	}
	if n > maxPackageZip {
		return "", "", errcode.Errorf(errcode.Download, "download "+u+": larger than 200 MiB")
	}
	return file, hex.EncodeToString(h.Sum(nil)), nil
}

// unzipPackage extracts a theme or plugin zip into dir and returns its
// folder: the single top-level folder, or dir itself. Absolute paths, ".."
// and links are refused; sizes are capped.
func unzipPackage(file, dir string) (string, error) {
	zr, err := zip.OpenReader(file)
	if err != nil {
		return "", errcode.New(errcode.Download, fmt.Errorf("open zip: %w", err))
	}
	defer zr.Close()
	if len(zr.File) > maxPackageFiles {
		return "", errcode.Errorf(errcode.InvalidInput, "zip has too many files")
	}
	var total uint64
	tops := map[string]bool{}
	for _, f := range zr.File {
		name := strings.TrimPrefix(f.Name, "./")
		if name == "" || strings.HasPrefix(name, "__MACOSX/") || path.Base(name) == ".DS_Store" {
			continue
		}
		clean := path.Clean(name)
		if path.IsAbs(name) || clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(name, "\\") {
			return "", errcode.Newf(errcode.InvalidInput, "zip entry %q is outside the package", f.Name)
		}
		mode := f.Mode()
		if mode&fs.ModeSymlink != 0 || (!mode.IsRegular() && !mode.IsDir()) {
			return "", errcode.Newf(errcode.InvalidInput, "zip entry %q is not a plain file", f.Name)
		}
		tops[strings.SplitN(clean, "/", 2)[0]] = true
		target := filepath.Join(dir, filepath.FromSlash(clean))
		if mode.IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", fmt.Errorf("extract: %w", err)
			}
			continue
		}
		total += f.UncompressedSize64
		if total > maxPackageTotal {
			return "", errcode.Errorf(errcode.InvalidInput, "zip is larger than 500 MiB unpacked")
		}
		if err := extractZipFile(f, target); err != nil {
			return "", err
		}
	}
	if len(tops) == 1 {
		for t := range tops {
			if st, err := os.Stat(filepath.Join(dir, t)); err == nil && st.IsDir() {
				return filepath.Join(dir, t), nil
			}
		}
	}
	if len(tops) == 0 {
		return "", errcode.Errorf(errcode.InvalidInput, "zip is empty")
	}
	return dir, nil
}

func extractZipFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("extract: %w", err)
	}
	rc, err := f.Open()
	if err != nil {
		return errcode.New(errcode.InvalidInput, fmt.Errorf("extract %s: %w", f.Name, err))
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}
	if _, err := io.Copy(out, io.LimitReader(rc, int64(f.UncompressedSize64)+1)); err != nil { //nolint:gosec // capped above
		_ = out.Close()
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("extract %s: %w", f.Name, err)
	}
	return nil
}

// HasGit reports whether the system has git (the Command Line Tools),
// without making macOS offer to install them.
func HasGit() bool {
	if err := exec.Command("/usr/bin/xcode-select", "-p").Run(); err != nil {
		return false
	}
	return exists(gitBinary)
}

// clonePackage clones a git package with the system git and hashes its
// files. key is the URL with the commit.
func (c *Core) clonePackage(ctx context.Context, p BlueprintPackage, work string) (key, sum, root string, err error) {
	if !HasGit() {
		return "", "", "", errcode.Errorf(errcode.InvalidInput,
			"git is not on this Mac (it comes with Apple's Command Line Tools: xcode-select --install); use a github or url source instead")
	}
	root = filepath.Join(work, "clone")
	args := []string{"-c", "core.hooksPath=/dev/null", "clone", "--quiet", "--depth", "1", "--no-recurse-submodules"}
	if p.Ref != "" {
		args = append(args, "--branch", p.Ref)
	}
	args = append(args, "--", p.Git, root)
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, gitBinary, args...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", "", errcode.New(errcode.Download, fmt.Errorf("git clone %s: %w: %s", p.Git, err, strings.TrimSpace(string(out))))
	}
	out, err := exec.CommandContext(ctx, gitBinary, "-C", root, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", "", "", fmt.Errorf("git rev-parse: %w", err)
	}
	commit := strings.TrimSpace(string(out))
	if err := os.RemoveAll(filepath.Join(root, ".git")); err != nil {
		return "", "", "", fmt.Errorf("remove .git: %w", err)
	}
	sum, err = treeSHA256(root)
	if err != nil {
		return "", "", "", err
	}
	return p.Git + "@" + commit, sum, root, nil
}

// treeSHA256 hashes a folder's paths and contents in a stable order.
// Links are refused, like in zips.
func treeSHA256(root string) (string, error) {
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return errcode.Errorf(errcode.InvalidInput, "package has a link: "+p)
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		f, err := os.Open(p) //nolint:gosec // inside the package being hashed
		if err != nil {
			return fmt.Errorf("open %s: %w", p, err)
		}
		defer f.Close()
		fmt.Fprintf(h, "%s\x00", filepath.ToSlash(rel))
		if _, err := io.Copy(h, f); err != nil {
			return fmt.Errorf("read %s: %w", p, err)
		}
		h.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("hash package: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// --- saving from a site ---

// BlueprintFromSite builds a blueprint from a WordPress site: version,
// language, themes and plugins from wordpress.org (pinned to the installed
// versions), permalinks, timezone and must-use plugins. Other themes and
// plugins come back as warnings: add their url or github by hand.
// Constants are not copied (they can hold secrets).
func (c *Core) BlueprintFromSite(ctx context.Context, siteName, name string) (Blueprint, []string, error) {
	bp := Blueprint{Name: name, Description: "Saved from " + siteName}
	var warnings []string
	site, err := c.Store.Site(ctx, siteName)
	if err != nil {
		return bp, nil, err
	}
	if !isWordPressDir(site.Path) {
		return bp, nil, errcode.Newf(errcode.InvalidInput, "site %q is not a WordPress site", siteName)
	}
	if !blueprintNameRe.MatchString(name) {
		return bp, nil, errcode.Newf(errcode.InvalidInput, "blueprint name %q must be lowercase letters, digits and dashes", name)
	}
	if v, err := c.wpOutput(ctx, site, "core", "version"); err == nil && bpVersionRe.MatchString(v) {
		bp.WordPress.Version = v
	}
	if l, err := c.wpOutput(ctx, site, "option", "get", "WPLANG"); err == nil && bpLocaleRe.MatchString(l) && l != "en_US" {
		bp.WordPress.Locale = l
	}
	for _, kind := range []string{"theme", "plugin"} {
		out, err := c.wpOutput(ctx, site, kind, "list", "--format=json", "--fields=name,status,version")
		if err != nil {
			return bp, nil, err
		}
		var items []struct {
			Name    string `json:"name"`
			Status  string `json:"status"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal([]byte(out), &items); err != nil {
			return bp, nil, fmt.Errorf("read %s list: %w", kind, err)
		}
		for _, it := range items {
			if it.Status == "must-use" || it.Status == "dropin" || !bpSlugRe.MatchString(it.Name) {
				continue
			}
			if kind == "plugin" && (it.Name == "hello" || it.Name == "akismet") {
				continue
			}
			if !c.onWordPressOrg(ctx, kind, it.Name) {
				warnings = append(warnings, fmt.Sprintf("%s %s is not on wordpress.org: add its url or github to the blueprint by hand", kind, it.Name))
				continue
			}
			active := it.Status == "active"
			pkg := BlueprintPackage{Slug: it.Name, Activate: &active}
			if bpVersionRe.MatchString(it.Version) {
				pkg.Version = it.Version
			}
			if kind == "theme" {
				if !active {
					pkg.Activate = nil
				}
				bp.Themes = append(bp.Themes, pkg)
			} else {
				bp.Plugins = append(bp.Plugins, pkg)
			}
		}
	}
	if p, err := c.wpOutput(ctx, site, "option", "get", "permalink_structure"); err == nil && strings.HasPrefix(p, "/") {
		bp.Options.Permalinks = p
	}
	if tz, err := c.wpOutput(ctx, site, "option", "get", "timezone_string"); err == nil && bpTimezoneRe.MatchString(tz) {
		bp.Options.Timezone = tz
	}
	mu := filepath.Join(site.Path, "wp-content", "mu-plugins")
	entries, _ := os.ReadDir(mu)
	for _, e := range entries {
		if e.IsDir() || !bpMUNameRe.MatchString(e.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(mu, e.Name()))
		if err != nil || len(data) > maxMUPluginSize || !strings.HasPrefix(strings.TrimSpace(string(data)), "<?php") {
			warnings = append(warnings, "must-use plugin "+e.Name()+" skipped (unreadable, too large or not PHP)")
			continue
		}
		bp.MUPlugins = append(bp.MUPlugins, BlueprintMUPlugin{Name: e.Name(), Content: string(data)})
	}
	return bp, warnings, bp.Validate()
}

func (c *Core) onWordPressOrg(ctx context.Context, kind, slug string) bool {
	q := url.Values{"action": {kind + "_information"}, "request[slug]": {slug}}
	body, err := c.httpGet(ctx, wporgAPI+"/"+kind+"s/info/1.2/?"+q.Encode(), "", "application/json", 4<<20)
	if err != nil {
		return false
	}
	var info struct {
		Slug string `json:"slug"`
	}
	return json.Unmarshal(body, &info) == nil && info.Slug == slug
}
