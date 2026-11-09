// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/agents"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/paths"
	"github.com/benbowdev/benbow/internal/trust"
)

// Benbow was called Covo until 2026-11-09 (docs/migrate-from-covo.md).
// `benbow migrate covo` moves a Covo installation over: its folder (sites,
// databases, snapshots), keychain items, the .test resolver file and the
// AI agents' MCP entries. The CA is new (its name is in the certificate):
// the old one is untrusted once Benbow serves the sites.

// Overrides for tests; a real migration uses Covo's own names.
const (
	EnvMigrateFrom        = "BENBOW_MIGRATE_FROM"
	EnvMigrateLabelPrefix = "BENBOW_MIGRATE_LABEL_PREFIX"
	EnvMigrateCAName      = "BENBOW_MIGRATE_CA_NAME"

	covoLabelPrefix = "dev.getcovo"
	covoCAName      = "Covo Local CA"
	covoMCPName     = "covo"
	covoROPrefix    = "covo_ro_"
	// covoMigrationDir holds, inside the moved folder, Covo's database as
	// it was, its CA root and the migration's state.
	covoMigrationDir = "covo-migration"
)

// keychainServices are the service suffixes Covo and Benbow keep secrets
// under (uninstall lists the same ones).
var keychainServices = []string{"mysql", "postgres", "remote", "github", appEnvService}

// covoSource is the Covo installation to migrate.
type covoSource struct {
	Home, Prefix, CAName string
}

func covoSourceFromEnv() (covoSource, error) {
	src := covoSource{Home: os.Getenv(EnvMigrateFrom), Prefix: os.Getenv(EnvMigrateLabelPrefix), CAName: os.Getenv(EnvMigrateCAName)}
	if src.Home == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return src, fmt.Errorf("resolve home directory: %w", err)
		}
		src.Home = filepath.Join(home, "Library", "Application Support", "covo")
	}
	if src.Prefix == "" {
		src.Prefix = covoLabelPrefix
	}
	if src.CAName == "" {
		src.CAName = covoCAName
	}
	return src, nil
}

// MigrateCovoOptions say how to migrate.
type MigrateCovoOptions struct {
	// DryRun only reports what would happen.
	DryRun bool
	// Finish only runs the last step (after a migration whose site check
	// failed, once the user has looked).
	Finish bool
	// NoTrust leaves the new CA untrusted and the old one as it is (tests:
	// trusting asks for a password).
	NoTrust bool
	// BeforePrompt is called before macOS may ask for a password.
	BeforePrompt func(what string)
}

// MigrationStep is one step of the migration.
type MigrationStep struct {
	Step string `json:"step"`
	// Status is planned (dry run), done, skipped or failed.
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// CovoMigration reports a migration from Covo.
type CovoMigration struct {
	From  string          `json:"from"`
	To    string          `json:"to"`
	Sites []string        `json:"sites"`
	Steps []MigrationStep `json:"steps"`
	// Warnings are steps that need the user (a password dialog cancelled,
	// a site that does not answer).
	Warnings []string `json:"warnings"`
	DryRun   bool     `json:"dry_run,omitempty"`
	// Finished means Covo's leftovers (keychain items, old CA, agent
	// entries) are gone; when false, `benbow migrate covo --finish` does it.
	Finished bool `json:"finished"`
}

func (m *CovoMigration) step(name, status, detail string) {
	m.Steps = append(m.Steps, MigrationStep{Step: name, Status: status, Detail: detail})
}

func (m *CovoMigration) warn(format string, a ...any) {
	m.Warnings = append(m.Warnings, fmt.Sprintf(format, a...))
}

// covoMigrationState is what a later --finish needs.
type covoMigrationState struct {
	From     string `json:"from"`
	Prefix   string `json:"prefix"`
	CAName   string `json:"ca_name"`
	Aside    string `json:"aside,omitempty"`
	NoTrust  bool   `json:"no_trust,omitempty"`
	Finished bool   `json:"finished"`
}

// CovoInstalled reports whether a Covo installation is there to migrate
// (the app offers the migration on its first start).
func CovoInstalled() (string, bool) {
	src, err := covoSourceFromEnv()
	if err != nil {
		return "", false
	}
	return src.Home, fileExists(filepath.Join(src.Home, "covo.db"))
}

// readCovo reads a Covo database without changing it: its sites and
// settings.
func readCovo(dbPath string) (sites []string, settings map[string]string, err error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, nil, fmt.Errorf("open %s: %w", dbPath, err)
	}
	defer db.Close()
	rows, err := db.Query(`SELECT name FROM sites ORDER BY name`)
	if err != nil {
		return nil, nil, errcode.New(errcode.InvalidInput, fmt.Errorf("%s is not a Covo database: %w", dbPath, err))
	}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("read sites: %w", err)
		}
		sites = append(sites, n)
	}
	rows.Close()
	settings = map[string]string{}
	rows, err = db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, nil, fmt.Errorf("read settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, nil, fmt.Errorf("read settings: %w", err)
		}
		settings[k] = v
	}
	return sites, settings, nil
}

// benbowHasSites reports whether the Benbow home already serves sites.
func benbowHasSites(l paths.Layout) (bool, error) {
	if !fileExists(l.DB) {
		return false, nil
	}
	sites, _, err := readCovo(l.DB) // same schema
	if err != nil {
		return false, err
	}
	return len(sites) > 0, nil
}

// MigrateFromCovo moves a Covo installation into Benbow. Until the folder
// and the keychain items are in place any failure puts everything back as
// it was; after that, a step that needs the user (a password dialog) only
// leaves a warning.
func MigrateFromCovo(ctx context.Context, opts MigrateCovoOptions) (CovoMigration, error) {
	src, err := covoSourceFromEnv()
	if err != nil {
		return CovoMigration{}, err
	}
	layout, err := paths.Default()
	if err != nil {
		return CovoMigration{}, err
	}
	res := CovoMigration{From: src.Home, To: layout.Root, Sites: []string{}, Steps: []MigrationStep{}, Warnings: []string{}, DryRun: opts.DryRun}
	if opts.Finish {
		return finishCovoOnly(ctx, layout, res, opts)
	}
	oldDB := filepath.Join(src.Home, "covo.db")
	if !fileExists(oldDB) {
		return res, errcode.Newf(errcode.NotFound, "no Covo installation in %s", src.Home)
	}
	sites, settings, err := readCovo(oldDB)
	if err != nil {
		return res, err
	}
	res.Sites = append(res.Sites, sites...)
	if has, err := benbowHasSites(layout); err != nil {
		return res, err
	} else if has {
		return res, errcode.Newf(errcode.Conflict, "Benbow already has sites in %s: the migration only moves Covo into an empty Benbow", layout.Root)
	}
	running := settings[settingRunning] == "1"
	_, testDomain := settings[settingDomain(suffixTest)]
	if opts.DryRun {
		res.step("stop Covo", "planned", "its launchd agents ("+src.Prefix+".*)")
		res.step("move the folder", "planned", src.Home+" → "+layout.Root+" (sites, databases, snapshots, logs; nothing is copied)")
		res.step("update saved paths", "planned", "")
		res.step("copy keychain items", "planned", src.Prefix+".* → "+launchd.Prefix()+".*")
		res.step("new local CA", "planned", "Benbow's own CA, trusted after one password dialog; Covo's is untrusted at the end")
		if testDomain {
			res.step(".test resolver", "planned", "rewritten for Benbow (one administrator password)")
		}
		if running {
			res.step("start Benbow", "planned", "")
			res.step("check the sites", "planned", "")
		}
		res.step("finish", "planned", "remove Covo's keychain items and CA, and point AI agents at Benbow")
		return res, nil
	}

	// From here on, until the keychain items are copied, undo puts Covo
	// back exactly as it was.
	var undo []func()
	rollback := func(cause error) (CovoMigration, error) {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		res.step("put Covo back", "done", "nothing was changed: open Covo to start it again")
		return res, cause
	}

	oldLC := launchd.New()
	oldLC.Prefix = src.Prefix
	if _, err := oldLC.StopAll(ctx); err != nil {
		res.step("stop Covo", "failed", err.Error())
		return res, err
	}
	res.step("stop Covo", "done", "")
	newLC := launchd.New()
	if _, err := newLC.StopAll(ctx); err != nil {
		res.step("stop Benbow", "failed", err.Error())
		return res, err
	}

	// The folder: an empty Benbow home moves aside, Covo's takes its place.
	state := covoMigrationState{From: src.Home, Prefix: src.Prefix, CAName: src.CAName, NoTrust: opts.NoTrust}
	if _, err := os.Stat(layout.Root); err == nil {
		state.Aside = layout.Root + ".before-covo-" + time.Now().Format("20060102-150405")
		if err := os.Rename(layout.Root, state.Aside); err != nil {
			return res, fmt.Errorf("move the empty Benbow folder aside: %w", err)
		}
		undo = append(undo, func() { _ = os.Rename(state.Aside, layout.Root) })
	}
	saved := filepath.Join(src.Home, "data", covoMigrationDir)
	if err := saveCovoFiles(src.Home, saved); err != nil {
		return rollback(err)
	}
	undo = append(undo, func() { _ = os.RemoveAll(saved) })
	if err := os.Rename(src.Home, layout.Root); err != nil {
		res.step("move the folder", "failed", err.Error())
		return rollback(fmt.Errorf("move %s to %s: %w", src.Home, layout.Root, err))
	}
	undo = append(undo, func() {
		// Covo's database exactly as it was (Benbow may have upgraded it).
		for _, f := range []string{"benbow.db", "benbow.db-wal", "benbow.db-shm"} {
			_ = os.Remove(filepath.Join(layout.Root, f))
		}
		_ = copyCovoDB(filepath.Join(layout.Root, "data", covoMigrationDir), layout.Root)
		_ = os.Rename(layout.Root, src.Home)
	})
	for _, f := range []string{"", "-wal", "-shm"} {
		if err := os.Rename(filepath.Join(layout.Root, "covo.db"+f), layout.DB+f); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return rollback(fmt.Errorf("rename the database: %w", err))
		}
	}
	_ = os.Remove(filepath.Join(layout.Root, "covo.lock"))
	if fi, err := os.Stat(filepath.Join(layout.Data, "covo.db")); err == nil && fi.Size() == 0 {
		_ = os.Remove(filepath.Join(layout.Data, "covo.db"))
	}
	old, _ := filepath.Glob(filepath.Join(layout.Launchd, src.Prefix+".*.plist"))
	for _, p := range old {
		_ = os.Remove(p)
	}
	res.step("move the folder", "done", layout.Root)

	c, err := Open(ctx)
	if err != nil {
		res.step("update saved paths", "failed", err.Error())
		return rollback(err)
	}
	defer func() { _ = c.Close() }()
	undo = append(undo, func() { _ = c.Close() })
	n, err := c.Store.ReplacePathPrefix(ctx, src.Home, layout.Root)
	if err != nil {
		res.step("update saved paths", "failed", err.Error())
		return rollback(err)
	}
	res.step("update saved paths", "done", strconv.FormatInt(n, 10)+" paths")

	copied, err := c.copyCovoKeychain(ctx, src.Prefix)
	undo = append(undo, func() {
		for _, it := range copied {
			_, _ = it.Delete(context.WithoutCancel(ctx))
		}
	})
	if err != nil {
		res.step("copy keychain items", "failed", err.Error())
		return rollback(err)
	}
	res.step("copy keychain items", "done", strconv.Itoa(len(copied))+" items")
	if err := c.writeCovoState(state); err != nil {
		return rollback(err)
	}

	// Past this point Benbow owns the installation: what is left asks the
	// user (passwords) or only cleans up, and never undoes the move.
	// Caddy's site certificates were signed by Covo's CA: Benbow's CA
	// signs new ones.
	_ = os.RemoveAll(filepath.Join(layout.Data, "caddy", "certificates"))
	if opts.NoTrust {
		if _, err := c.ensureCA(); err != nil {
			res.warn("new CA: %v", err)
		}
		res.step("new local CA", "done", "not trusted yet: run `benbow trust`")
	} else {
		if opts.BeforePrompt != nil {
			opts.BeforePrompt("trust Benbow's local certificate authority")
		}
		if _, err := c.Trust(ctx, nil); err != nil {
			res.step("new local CA", "failed", err.Error())
			res.warn("Browsers will warn on your sites until you run `benbow trust` (or press Trust in the app): %v", err)
		} else {
			res.step("new local CA", "done", "trusted")
		}
	}

	if testDomain {
		if err := c.replaceCovoResolver(ctx, src.Home, opts.BeforePrompt); err != nil {
			res.step(".test resolver", "failed", err.Error())
			res.warn(".test addresses need the resolver again: run `benbow domains enable test`: %v", err)
		} else {
			res.step(".test resolver", "done", "")
		}
	}

	checked := true
	if running {
		if _, err := c.Start(ctx, 0); err != nil {
			res.step("start Benbow", "failed", err.Error())
			res.warn("Benbow did not start: %v", err)
			checked = false
		} else {
			res.step("start Benbow", "done", "")
			for _, w := range c.checkMigratedSites(ctx) {
				checked = false
				res.warn("%s", w)
			}
			status := "done"
			if !checked {
				status = "failed"
			}
			res.step("check the sites", status, "")
		}
	}
	if !checked {
		res.step("finish", "skipped", "Covo's keychain items and CA are kept: run `benbow migrate covo --finish` once your sites work")
		return res, nil
	}
	return c.finishCovo(ctx, state, res, opts)
}

// saveCovoFiles keeps Covo's database and CA root as they were, inside
// Covo's folder (so they move with it).
func saveCovoFiles(home, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}
	if err := copyCovoDB(home, dir); err != nil {
		return err
	}
	root := filepath.Join(home, "data", "ca", "root.crt")
	if fileExists(root) {
		if err := copyFile(root, filepath.Join(dir, "old-root.crt")); err != nil {
			return err
		}
	}
	return nil
}

// copyCovoDB copies covo.db and its WAL files from one folder to another.
func copyCovoDB(from, to string) error {
	for _, f := range []string{"covo.db", "covo.db-wal", "covo.db-shm"} {
		if !fileExists(filepath.Join(from, f)) {
			continue
		}
		if err := copyFile(filepath.Join(from, f), filepath.Join(to, f)); err != nil {
			return err
		}
	}
	return nil
}

func (c *Core) covoStatePath() string {
	return filepath.Join(c.Layout.Data, covoMigrationDir, "state.json")
}

func (c *Core) writeCovoState(s covoMigrationState) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	_, err := fsutil.WriteAtomic(c.covoStatePath(), b, 0o600)
	return err
}

// copyCovoKeychain copies Covo's secrets to Benbow's services, with the
// same accounts. MySQL's read-only users are left out: Benbow makes its own
// on first use.
func (c *Core) copyCovoKeychain(ctx context.Context, oldPrefix string) ([]keychain.Item, error) {
	path, err := keychain.LoginPath()
	if err != nil {
		return nil, err
	}
	var copied []keychain.Item
	for _, svc := range keychainServices {
		old := keychain.Item{Path: path, Service: oldPrefix + "." + svc, Run: c.keychainRun}
		entries, err := old.Entries(ctx)
		if err != nil {
			return copied, err
		}
		for _, e := range entries {
			if strings.HasPrefix(e.Account, covoROPrefix) {
				continue
			}
			from := old
			from.Account = e.Account
			secret, err := from.Get(ctx)
			if err != nil {
				return copied, fmt.Errorf("read %s %s: %w", from.Service, e.Account, err)
			}
			label := strings.ReplaceAll(strings.ReplaceAll(e.Label, oldPrefix, c.launchctl.Prefix), "Covo", "Benbow")
			to := keychain.Item{Path: path, Service: c.launchctl.Prefix + "." + svc, Account: e.Account, Label: label, Run: c.keychainRun}
			if err := to.Set(ctx, secret); err != nil {
				return copied, err
			}
			copied = append(copied, to)
		}
	}
	return copied, nil
}

// replaceCovoResolver rewrites Covo's /etc/resolver/test for Benbow, if it
// is Covo's for that home.
func (c *Core) replaceCovoResolver(ctx context.Context, covoHome string, beforePrompt func(string)) error {
	m := newResolverManager(c.Layout.Root, c.Layout.Tmp)
	b, err := os.ReadFile(m.Path(suffixTest))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", m.Path(suffixTest), err)
	}
	home, created, port, ok := parseCovoResolver(string(b))
	if !ok || home != covoHome {
		return errcode.Newf(errcode.Conflict, "%s was not written by Covo for %s: left alone", m.Path(suffixTest), covoHome)
	}
	if beforePrompt != nil {
		beforePrompt("update the .test resolver")
	}
	return m.Replace(ctx, suffixTest, port, created)
}

// parseCovoResolver reads a resolver file written by Covo.
func parseCovoResolver(content string) (home string, created bool, port int, ok bool) {
	sc := bufio.NewScanner(strings.NewReader(content))
	first := true
	for sc.Scan() {
		line := sc.Text()
		if first {
			if !strings.HasPrefix(line, "# Generated by Covo.") {
				return "", false, 0, false
			}
			first = false
			continue
		}
		switch {
		case strings.HasPrefix(line, "# covo-home: "):
			home = strings.TrimPrefix(line, "# covo-home: ")
		case strings.HasPrefix(line, "# covo-created-dir: "):
			created = strings.TrimPrefix(line, "# covo-created-dir: ") == "yes"
		case strings.HasPrefix(line, "port "):
			port, _ = strconv.Atoi(strings.TrimPrefix(line, "port "))
		}
	}
	return home, created, port, !first && home != "" && port > 0
}

// checkMigratedSites asks each PHP site for its home page through Benbow.
// Any HTTP answer will do (a 500 is the site's own business); a failed
// request is a warning.
func (c *Core) checkMigratedSites(ctx context.Context) []string {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return []string{err.Error()}
	}
	var warnings []string
	for _, s := range sites {
		if !s.Enabled || s.IsApp() {
			continue
		}
		if _, err := c.FetchPage(ctx, FetchRequest{Site: s.Name, Path: "/", Method: "HEAD"}); err != nil {
			warnings = append(warnings, fmt.Sprintf("%s does not answer through Benbow: %v", s.Name, err))
		}
	}
	return warnings
}

// finishCovoOnly is `benbow migrate covo --finish`.
func finishCovoOnly(ctx context.Context, layout paths.Layout, res CovoMigration, opts MigrateCovoOptions) (CovoMigration, error) {
	c, err := Open(ctx)
	if err != nil {
		return res, err
	}
	defer func() { _ = c.Close() }()
	b, err := os.ReadFile(c.covoStatePath())
	if err != nil {
		return res, errcode.Newf(errcode.NotFound, "no migration from Covo to finish in %s", layout.Root)
	}
	var state covoMigrationState
	if err := json.Unmarshal(b, &state); err != nil {
		return res, fmt.Errorf("read %s: %w", c.covoStatePath(), err)
	}
	res.From = state.From
	if state.Finished {
		res.Finished = true
		res.step("finish", "skipped", "already done")
		return res, nil
	}
	if opts.DryRun {
		res.step("finish", "planned", "remove Covo's keychain items and CA, and point AI agents at Benbow")
		return res, nil
	}
	return c.finishCovo(ctx, state, res, opts)
}

// finishCovo removes what Covo leaves once Benbow serves the sites: its
// keychain items, its CA's trust, its MySQL read-only users and the AI
// agents' "covo" entries.
func (c *Core) finishCovo(ctx context.Context, state covoMigrationState, res CovoMigration, opts MigrateCovoOptions) (CovoMigration, error) {
	path, err := keychain.LoginPath()
	if err != nil {
		return res, err
	}
	removed := 0
	for _, svc := range keychainServices {
		old := keychain.Item{Path: path, Service: state.Prefix + "." + svc, Run: c.keychainRun}
		entries, err := old.Entries(ctx)
		if err != nil {
			res.warn("Covo's keychain items: %v", err)
			continue
		}
		for _, e := range entries {
			it := old
			it.Account = e.Account
			if ok, err := it.Delete(ctx); err != nil {
				res.warn("remove %s %s from the keychain: %v", it.Service, e.Account, err)
			} else if ok {
				removed++
			}
		}
	}
	res.step("remove Covo's keychain items", "done", strconv.Itoa(removed)+" items")

	oldRoot := filepath.Join(c.Layout.Data, covoMigrationDir, "old-root.crt")
	switch {
	case state.NoTrust || opts.NoTrust:
		res.step("untrust Covo's CA", "skipped", "")
	case !fileExists(oldRoot):
		res.step("untrust Covo's CA", "skipped", "no certificate left")
	default:
		kc, err := trust.Login()
		if err == nil {
			if opts.BeforePrompt != nil {
				opts.BeforePrompt("remove Covo's local certificate authority")
			}
			_, err = removeCAs(ctx, kc, state.CAName, []string{oldRoot})
		}
		if err != nil {
			res.step("untrust Covo's CA", "failed", err.Error())
			res.warn("Covo's CA is still trusted: remove %q in Keychain Access: %v", state.CAName, err)
		} else {
			res.step("untrust Covo's CA", "done", "")
		}
	}

	if st, err := c.mysqlStatus(ctx); err == nil && st.Enabled && st.Running {
		if n, err := c.dropCovoReadOnlyUsers(ctx); err != nil {
			res.warn("MySQL read-only users of Covo: %v", err)
		} else {
			res.step("remove Covo's MySQL read-only users", "done", strconv.Itoa(n)+" users")
		}
	}

	if c.launchctl.Prefix == launchd.DefaultPrefix || os.Getenv("BENBOW_AGENTS_HOME") != "" {
		env := agents.DefaultEnv()
		moved := 0
		for _, a := range agents.Detect(ctx, env) {
			if !agents.HasServer(ctx, env, a.ID, covoMCPName) {
				continue
			}
			if _, err := c.ConnectAgent(ctx, a.ID); err != nil {
				res.warn("%s: connect Benbow: %v", a.Name, err)
				continue
			}
			if err := agents.RemoveServer(ctx, env, a.ID, covoMCPName); err != nil {
				res.warn("%s: remove the covo entry: %v", a.Name, err)
				continue
			}
			moved++
		}
		res.step("AI agents", "done", strconv.Itoa(moved)+" moved from covo to benbow")
	}

	if state.Aside != "" {
		_ = os.RemoveAll(state.Aside)
	}
	dir := filepath.Join(c.Layout.Data, covoMigrationDir)
	for _, f := range []string{"covo.db", "covo.db-wal", "covo.db-shm", "old-root.crt"} {
		_ = os.Remove(filepath.Join(dir, f))
	}
	state.Finished = true
	if err := c.writeCovoState(state); err != nil {
		return res, err
	}
	res.Finished = true
	res.step("finish", "done", "Covo.app can go to the Trash")
	return res, nil
}

// dropCovoReadOnlyUsers removes Covo's read-only MySQL users (Benbow makes
// its own).
func (c *Core) dropCovoReadOnlyUsers(ctx context.Context) (int, error) {
	out, err := c.rootQuery(ctx, `SELECT CONCAT("'", User, "'@'", Host, "'") FROM mysql.user WHERE User LIKE 'covo\_ro\_%'`)
	if err != nil {
		return 0, err
	}
	var users []string
	for _, l := range out {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, "'"+covoROPrefix) {
			users = append(users, l)
		}
	}
	if len(users) == 0 {
		return 0, nil
	}
	_, err = c.rootQuery(ctx, "DROP USER IF EXISTS "+strings.Join(users, ", ")+";")
	return len(users), err
}
