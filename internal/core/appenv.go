// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/store"
)

// An app's environment (docs/apps.md): what Benbow knows about the site
// (its URL, database, Redis, Mailpit) plus the user's own variables, kept
// in the login keychain. Values only ever reach the app's process: never a
// plist, a file, a command line, the API or an agent.

const (
	appEnvService = "appenv"
	// maxAppEnv bounds the user's variables (one keychain item).
	maxAppEnv = 32 << 10
)

var reEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

// reservedEnv are names Benbow sets itself and the user may not replace.
var reservedEnv = []string{"PORT", "HOME", "SHELL"}

func checkEnvName(name string) error {
	if !reEnvName.MatchString(name) {
		return errcode.Newf(errcode.InvalidInput, "%q is not a variable name (letters, digits and _, not starting with a digit)", name)
	}
	if slices.Contains(reservedEnv, name) || strings.HasPrefix(name, "BENBOW_") {
		return errcode.Newf(errcode.InvalidInput, "%s is set by Benbow and cannot be changed", name)
	}
	return nil
}

func (c *Core) appEnvItem(site string) (keychain.Item, error) {
	p, err := keychain.LoginPath()
	return keychain.Item{
		Path: p, Service: c.launchctl.Prefix + "." + appEnvService, Account: site,
		Label: "Benbow app variables (" + site + ")", Run: c.keychainRun,
	}, err
}

// appEnvValues returns a site's own variables (none when it has none).
func (c *Core) appEnvValues(ctx context.Context, site string) (map[string]string, error) {
	item, err := c.appEnvItem(site)
	if err != nil {
		return nil, err
	}
	raw, err := item.Get(ctx)
	if errors.Is(err, keychain.ErrNotFound) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	vars := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &vars); err != nil {
		return nil, errcode.Newf(errcode.Service, "the keychain item of %s's variables is damaged: %v", site, err)
	}
	return vars, nil
}

// AppEnv is an app's own variables: names, and values only when asked for
// from the CLI (never over the API).
type AppEnv struct {
	Site   string            `json:"site"`
	Names  []string          `json:"names"`
	Values map[string]string `json:"values,omitempty"`
	// Provided are the variables Benbow sets itself for this site now.
	Provided []string `json:"provided"`
	// Restarted is set when a change restarted the running app.
	Restarted bool `json:"restarted,omitempty"`
}

func (c *Core) appEnvSite(ctx context.Context, name string) (store.Site, error) {
	s, err := c.Store.Site(ctx, name)
	if err != nil {
		return s, err
	}
	if !s.IsApp() || s.AppMode != store.AppRun {
		return s, errcode.Newf(errcode.InvalidInput, "%s is not an app Benbow starts: variables are for the commands Benbow runs", name)
	}
	return s, nil
}

// GetAppEnv lists an app's variables; show includes their values.
func (c *Core) GetAppEnv(ctx context.Context, name string, show bool) (AppEnv, error) {
	s, err := c.appEnvSite(ctx, name)
	if err != nil {
		return AppEnv{}, err
	}
	vars, err := c.appEnvValues(ctx, name)
	if err != nil {
		return AppEnv{}, err
	}
	res := AppEnv{Site: name, Names: sortedKeys(vars)}
	if show {
		res.Values = vars
	}
	provided, _ := c.providedEnv(ctx, s)
	for _, kv := range provided {
		k, _, _ := strings.Cut(kv, "=")
		res.Provided = append(res.Provided, k)
	}
	return res, nil
}

// SetAppEnv sets and removes an app's own variables, then restarts the app
// if it runs so it sees them.
func (c *Core) SetAppEnv(ctx context.Context, name string, set map[string]string, unset []string) (AppEnv, error) {
	var restarted bool
	err := c.locked(ctx, func() error {
		if _, err := c.appEnvSite(ctx, name); err != nil {
			return err
		}
		vars, err := c.appEnvValues(ctx, name)
		if err != nil {
			return err
		}
		for _, k := range unset {
			if err := checkEnvName(k); err != nil {
				return err
			}
			delete(vars, k)
		}
		for k, v := range set {
			if err := checkEnvName(k); err != nil {
				return err
			}
			if strings.ContainsRune(v, 0) {
				return errcode.Newf(errcode.InvalidInput, "the value of %s contains a NUL byte", k)
			}
			vars[k] = v
		}
		item, err := c.appEnvItem(name)
		if err != nil {
			return err
		}
		if len(vars) == 0 {
			if _, err := item.Delete(ctx); err != nil {
				return err
			}
		} else {
			b, _ := json.Marshal(vars)
			if len(b) > maxAppEnv {
				return errcode.Newf(errcode.InvalidInput, "the variables take more than %d KB", maxAppEnv>>10)
			}
			if err := item.Set(ctx, string(b)); err != nil {
				return err
			}
		}
		restarted, err = c.restartIfLoaded(ctx, name)
		return err
	})
	if err != nil {
		return AppEnv{}, err
	}
	res, err := c.GetAppEnv(ctx, name, false)
	res.Restarted = restarted
	return res, err
}

// restartIfLoaded restarts an app whose agent is loaded (running or
// ended), so a change of its environment applies. Callers hold the lock.
func (c *Core) restartIfLoaded(ctx context.Context, name string) (bool, error) {
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return false, err
	}
	if _, ok := loaded[c.appLabel(name)]; !ok {
		return false, nil
	}
	if err := c.launchctl.Bootout(ctx, c.appLabel(name)); err != nil {
		return false, err
	}
	_, err = c.reconcile(ctx)
	return err == nil, err
}

// removeAppEnv deletes a removed app's variables.
func (c *Core) removeAppEnv(ctx context.Context, name string) {
	if item, err := c.appEnvItem(name); err == nil {
		_, _ = item.Delete(ctx)
	}
}

// providedEnv is what Benbow tells an app about its site: its URL and, when
// they are on, the database, Redis and Mailpit. A part that cannot be read
// (MySQL stopped, keychain locked) is left out and reported in warnings.
func (c *Core) providedEnv(ctx context.Context, s store.Site) (env, warnings []string) {
	if p, _, err := c.ports(ctx); err == nil {
		env = append(env, "APP_URL="+siteURL(s, p.HTTPS))
	}
	if s.Database != "" {
		if db, err := c.siteDatabase(ctx, s); err != nil {
			warnings = append(warnings, "DATABASE_URL left out: "+err.Error())
		} else {
			env = append(env, "DATABASE_URL="+databaseURL(db))
		}
	}
	if on, err := c.redisEnabled(ctx); err == nil && on {
		if port, err := c.redisPort(ctx); err == nil {
			env = append(env, "REDIS_URL=redis://127.0.0.1:"+strconv.Itoa(port))
		}
	}
	if on, err := c.mailEnabled(ctx); err == nil && on {
		if smtp, _, err := c.mailPorts(ctx); err == nil {
			port := strconv.Itoa(smtp)
			env = append(env, "SMTP_HOST=127.0.0.1", "SMTP_PORT="+port, "MAIL_HOST=127.0.0.1", "MAIL_PORT="+port,
				"SMTP_URL=smtp://127.0.0.1:"+port)
		}
	}
	return env, warnings
}

// databaseURL is the URL most frameworks read (Prisma, Rails, dj-database-
// url, SQLAlchemy with a driver prefix).
func databaseURL(db SiteDatabase) string {
	scheme := "mysql"
	if db.Engine == EnginePostgres {
		scheme = "postgresql"
	}
	host := db.Host
	if host == "" || host == "localhost" {
		host = "127.0.0.1"
	}
	u := url.URL{Scheme: scheme, User: url.UserPassword(db.User, db.Password),
		Host: net.JoinHostPort(host, strconv.Itoa(db.Port)), Path: "/" + db.Database}
	return u.String()
}

// appServicesHash fingerprints what providedEnv depends on, without
// secrets, for the app agents' plists: linking a database or turning Redis
// on restarts the apps that see it.
func (c *Core) appServicesHash(ctx context.Context) string {
	var parts []string
	if p, _, err := c.ports(ctx); err == nil {
		parts = append(parts, "https="+strconv.Itoa(p.HTTPS))
	}
	if on, _ := c.redisEnabled(ctx); on {
		port, _ := c.redisPort(ctx)
		parts = append(parts, "redis="+strconv.Itoa(port))
	}
	if on, _ := c.mailEnabled(ctx); on {
		smtp, _, _ := c.mailPorts(ctx)
		parts = append(parts, "mail="+strconv.Itoa(smtp))
	}
	if st, err := c.mysqlStatus(ctx); err == nil && st.Enabled {
		parts = append(parts, "mysql="+strconv.Itoa(st.Port))
	}
	if st, err := c.postgresStatus(ctx); err == nil && st.Enabled {
		parts = append(parts, "pgsql="+strconv.Itoa(st.Port))
	}
	return strings.Join(parts, ",")
}

// appEnvironment is Benbow's variables then the user's (theirs win), for the
// wrapper; it also returns the names, for the log.
func (c *Core) appEnvironment(ctx context.Context, s store.Site) (env, names, warnings []string) {
	env, warnings = c.providedEnv(ctx, s)
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		names = append(names, k)
	}
	vars, err := c.appEnvValues(ctx, s.Name)
	if err != nil {
		warnings = append(warnings, "your variables left out: "+err.Error())
	}
	for _, k := range sortedKeys(vars) {
		if checkEnvName(k) != nil {
			continue
		}
		env = append(env, k+"="+vars[k])
		names = append(names, k)
	}
	return env, names, warnings
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// describeEnv is the log line listing the variables an app gets (names
// only).
func describeEnv(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return fmt.Sprintf("variables: %s", strings.Join(names, ", "))
}
