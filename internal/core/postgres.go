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
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/ports"
	"github.com/getcovo/covo/internal/postgres"
	"github.com/getcovo/covo/internal/store"
)

// PostgreSQL works like MySQL: optional, off by default, data under the
// Covo folder, 127.0.0.1 only, superuser password in the login keychain.
// Major versions never change on their own (their data directories are
// not compatible).

const (
	postgresService      = "postgres"
	postgresSeries       = "18"
	settingPostgres      = "postgres_enabled"
	settingPostgresPort  = "postgres_port"
	postgresInitStageDir = "postgres.init"
)

// PostgresStatus describes Covo's PostgreSQL server.
type PostgresStatus struct {
	Enabled        bool   `json:"enabled"`
	Version        string `json:"version,omitempty"`
	Host           string `json:"host,omitempty"`
	Port           int    `json:"port,omitempty"`
	User           string `json:"user,omitempty"`
	DataDir        string `json:"data_dir"`
	Initialized    bool   `json:"initialized"`
	PasswordStored bool   `json:"password_stored"`
	Running        bool   `json:"running"`
}

// PostgresResult reports what enable or disable did.
type PostgresResult struct {
	PostgresStatus
	Changed bool `json:"changed"`
	Applied bool `json:"applied"`
}

func (c *Core) postgresDataDir() string { return filepath.Join(c.Layout.Data, "postgres") }

func postgresKeychain(prefix string) (mysql.Keychain, error) {
	kc, err := mysqlKeychain(prefix)
	kc.Service = prefix + "." + postgresService
	return kc, err
}

func (c *Core) postgresEnabled(ctx context.Context) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingPostgres)
	return ok, err
}

func (c *Core) postgresInitialized() (bool, error) {
	dir := c.postgresDataDir()
	if _, err := os.Stat(filepath.Join(dir, "PG_VERSION")); err == nil {
		return true, nil
	}
	if _, err := os.Lstat(dir); err == nil {
		return false, errcode.Errorf(errcode.Conflict, dir+" exists but holds no PostgreSQL data directory; move it away and run `covo postgres enable` again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	return false, nil
}

func (c *Core) postgresPort(ctx context.Context) (int, error) {
	return c.storedPort(ctx, settingPostgresPort, postgres.DefaultPort)
}

// storedPort returns the port stored under setting, assigning the first
// free one from def on first use.
func (c *Core) storedPort(ctx context.Context, setting string, def int) (int, error) {
	v, ok, err := c.Store.Setting(ctx, setting)
	if err != nil {
		return 0, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", setting, v, err)
		}
		return port, nil
	}
	port, err := ports.FirstFree(def, portSearchSpan)
	if err != nil {
		return 0, err
	}
	return port, c.Store.SetSetting(ctx, setting, strconv.Itoa(port))
}

// setStoredPort changes a service port; it must be free.
func (c *Core) setStoredPort(ctx context.Context, setting, what string, port int) error {
	if port < 1024 || port > 65535 {
		return errcode.Newf(errcode.InvalidInput, "%s port must be between 1024 and 65535, got %d", what, port)
	}
	if v, ok, err := c.Store.Setting(ctx, setting); err != nil {
		return err
	} else if ok && v == strconv.Itoa(port) {
		return nil
	}
	if !ports.Free(port) {
		return errcode.Newf(errcode.PortInUse, "port %d is in use by %s", port, ports.Describe(ports.Owners(ctx, port)))
	}
	return c.Store.SetSetting(ctx, setting, strconv.Itoa(port))
}

// ensurePostgres installs the pinned PostgreSQL and checks it runs.
func (c *Core) ensurePostgres(ctx context.Context) (dir, version string, err error) {
	art, err := c.pinned(ctx, "postgres", postgresSeries)
	if errcode.Code(err) == errcode.NotFound {
		return "", "", errcode.Errorf(errcode.Unsupported, "PostgreSQL is not available yet: run `covo update check` to fetch the newest manifest")
	}
	if err != nil {
		return "", "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", "", err
	}
	bin := filepath.Join(inst.Dir, "bin", "postgres")
	out, err := runQuiet(ctx, bin, "--version")
	if err != nil {
		return "", "", errcode.New(errcode.Service, fmt.Errorf("run %s --version: %w", bin, err))
	}
	if !strings.Contains(out, "(PostgreSQL) "+art.Version) {
		return "", "", errcode.Newf(errcode.Checksum, "postgres reports %q, expected %s", firstLine(out), art.Version)
	}
	return inst.Dir, art.Version, nil
}

func (c *Core) initPostgres(ctx context.Context, baseDir string) error {
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return err
	}
	password := mysql.NewPassword()
	if err := kc.Set(ctx, password); err != nil {
		return err
	}
	stage := filepath.Join(c.Layout.Data, postgresInitStageDir)
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove %s: %w", stage, err)
	}
	defer os.RemoveAll(stage) // no-op after the rename
	if err := postgres.Initialize(ctx, filepath.Join(baseDir, "bin"), stage, password, c.Layout.Tmp); err != nil {
		return err
	}
	if err := os.Rename(stage, c.postgresDataDir()); err != nil {
		return fmt.Errorf("move PostgreSQL data directory into place: %w", err)
	}
	return nil
}

func (c *Core) preparePostgres(ctx context.Context) (agent launchd.Agent, port int, err error) {
	baseDir, _, err := c.ensurePostgres(ctx)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if port, err = c.postgresPort(ctx); err != nil {
		return launchd.Agent{}, 0, err
	}
	initialized, err := c.postgresInitialized()
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if !initialized {
		if err := c.initPostgres(ctx, baseDir); err != nil {
			return launchd.Agent{}, 0, err
		}
	} else {
		kc, err := postgresKeychain(c.launchctl.Prefix)
		if err != nil {
			return launchd.Agent{}, 0, err
		}
		if ok, err := kc.Exists(ctx); err != nil {
			return launchd.Agent{}, 0, err
		} else if !ok {
			return launchd.Agent{}, 0, errcode.Newf(errcode.Conflict,
				"the PostgreSQL data directory %s exists but its password is not in the keychain (item %q)", c.postgresDataDir(), kc.Service)

		}
	}
	out, errLog := c.logPaths(postgresService)
	return launchd.Agent{
		Label:   c.labelFor(postgresService),
		Program: filepath.Join(baseDir, "bin", "postgres"),
		Args: []string{
			"-D", c.postgresDataDir(), "-p", strconv.Itoa(port),
			"-c", "listen_addresses=127.0.0.1",
			"-c", "unix_socket_directories=" + filepath.Dir(c.Layout.Socket("x")),
			"-c", "unix_socket_permissions=0700",
		},
		WorkDir:     c.Layout.Root,
		StdoutLog:   out,
		StderrLog:   errLog,
		ExitTimeout: mysqlExitTimeout,
	}, port, nil
}

func (c *Core) postgresAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	enabled, err := c.postgresEnabled(ctx)
	if err != nil || !enabled {
		return nil, 0, err
	}
	agent, port, err := c.preparePostgres(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !loaded[agent.Label].Running && !ports.Free(port) {
		return nil, 0, errcode.Newf(errcode.PortInUse,
			"PostgreSQL port %d is in use by %s; stop it or run `covo postgres enable --port <free port>`",
			port, ports.Describe(ports.Owners(ctx, port)))

	}
	return []launchd.Agent{agent}, port, nil
}

func (c *Core) postgresStatus(ctx context.Context) (PostgresStatus, error) {
	st := PostgresStatus{DataDir: c.postgresDataDir()}
	var err error
	if st.Enabled, err = c.postgresEnabled(ctx); err != nil {
		return st, err
	}
	_, statErr := os.Stat(filepath.Join(st.DataDir, "PG_VERSION"))
	st.Initialized = statErr == nil
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return st, err
	}
	if st.PasswordStored, err = kc.Exists(ctx); err != nil {
		return st, err
	}
	if !st.Enabled {
		return st, nil
	}
	st.Version = c.pinnedVersion(ctx, "postgres", postgresSeries)
	if st.Port, err = c.postgresPort(ctx); err != nil {
		return st, err
	}
	st.Host, st.User = "127.0.0.1", postgres.Superuser
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return st, err
	}
	st.Running = loaded[c.labelFor(postgresService)].Running
	return st, nil
}

// Postgres reports the state of Covo's PostgreSQL server.
func (c *Core) Postgres(ctx context.Context) (PostgresStatus, error) { return c.postgresStatus(ctx) }

// EnablePostgres installs and initializes PostgreSQL and starts it if Covo
// is running. port, if non-zero, changes the port first.
func (c *Core) EnablePostgres(ctx context.Context, port int) (PostgresResult, error) {
	var res PostgresResult
	err := c.locked(ctx, func() error {
		if port != 0 {
			if err := c.setStoredPort(ctx, settingPostgresPort, "PostgreSQL", port); err != nil {
				return err
			}
		}
		was, err := c.postgresEnabled(ctx)
		if err != nil {
			return err
		}
		if _, _, err := c.preparePostgres(ctx); err != nil {
			return err
		}
		if err := c.Store.SetSetting(ctx, settingPostgres, "1"); err != nil {
			return err
		}
		res.Changed = !was || port != 0
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.PostgresStatus, err = c.postgresStatus(ctx)
		return err
	})
	return res, err
}

// DisablePostgres stops PostgreSQL; data and password are kept.
func (c *Core) DisablePostgres(ctx context.Context) (PostgresResult, error) {
	var res PostgresResult
	err := c.locked(ctx, func() error {
		was, err := c.postgresEnabled(ctx)
		if err != nil {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingPostgres); err != nil {
			return err
		}
		res.Changed = was
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.PostgresStatus, err = c.postgresStatus(ctx)
		return err
	})
	return res, err
}

// PostgresCredentials returns how to connect as the superuser.
func (c *Core) PostgresCredentials(ctx context.Context) (MySQLCredentials, error) {
	st, err := c.postgresStatus(ctx)
	if err != nil {
		return MySQLCredentials{}, err
	}
	if !st.Enabled {
		return MySQLCredentials{}, errcode.Errorf(errcode.NotFound, "PostgreSQL is not enabled: run `covo postgres enable`")
	}
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return MySQLCredentials{}, err
	}
	pw, err := kc.Get(ctx)
	if errors.Is(err, mysql.ErrNoPassword) {
		return MySQLCredentials{}, errcode.New(errcode.NotFound, err)
	}
	if err != nil {
		return MySQLCredentials{}, err
	}
	return MySQLCredentials{Host: st.Host, Port: st.Port, Socket: filepath.Dir(c.Layout.Socket("x")), User: st.User, Password: pw}, nil
}

// postgresRoot returns psql, the port and the superuser password.
// PostgreSQL must be running.
func (c *Core) postgresRoot(ctx context.Context) (psql string, port int, password string, err error) {
	st, err := c.postgresStatus(ctx)
	if err != nil {
		return "", 0, "", err
	}
	if !st.Enabled {
		return "", 0, "", errcode.Errorf(errcode.Unsupported, "PostgreSQL is off: turn it on first (covo postgres enable)")
	}
	if !st.Running {
		return "", 0, "", errcode.Errorf(errcode.Conflict, "PostgreSQL is not running: start Covo first (covo start)")
	}
	dir, _, err := c.ensurePostgres(ctx)
	if err != nil {
		return "", 0, "", err
	}
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return "", 0, "", err
	}
	if password, err = kc.Get(ctx); err != nil {
		return "", 0, "", err
	}
	return filepath.Join(dir, "bin", "psql"), st.Port, password, nil
}

// pgQuery runs SQL as the superuser on db and returns the rows.
func (c *Core) pgQuery(ctx context.Context, db, sql string) ([]string, error) {
	psql, port, pw, err := c.postgresRoot(ctx)
	if err != nil {
		return nil, err
	}
	out, err := postgres.Query(ctx, psql, port, pw, db, sql, c.Layout.Tmp)
	if err != nil {
		return nil, err
	}
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l != "" {
			rows = append(rows, l)
		}
	}
	return rows, nil
}

// PostgresDatabases lists the user databases on Covo's PostgreSQL.
func (c *Core) PostgresDatabases(ctx context.Context) ([]string, error) {
	rows, err := c.pgQuery(ctx, "postgres", "SELECT datname FROM pg_database WHERE NOT datistemplate AND datname <> 'postgres' ORDER BY 1;")
	if err != nil {
		return nil, err
	}
	if rows == nil {
		rows = []string{}
	}
	return rows, nil
}

// createPostgresDatabase creates a database and a role of the same name
// that owns it and may only connect to it, with a random password in the
// keychain, and links it to the site.
func (c *Core) createPostgresDatabase(ctx context.Context, name, db string) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if site.Database != "" {
			return errcode.Newf(errcode.Conflict, "%s already uses database %s", name, site.Database)
		}
		if db == "" {
			db = DefaultDatabaseName(name)
		}
		if !mysql.ValidName(db, 63) || db == postgres.Superuser || strings.HasPrefix(db, "pg_") {
			return errcode.Newf(errcode.InvalidInput, "database name %q must be up to 63 lowercase letters, digits and underscores", db)
		}
		user := db
		dbs, err := c.PostgresDatabases(ctx)
		if err != nil {
			return err
		}
		if slices.Contains(dbs, db) {
			return errcode.Newf(errcode.Conflict, "database %s already exists: link it instead", db)
		}
		roles, err := c.pgQuery(ctx, "postgres", "SELECT rolname FROM pg_roles WHERE rolname = "+postgres.Literal(user)+";")
		if err != nil {
			return err
		}
		if len(roles) > 0 {
			return errcode.Newf(errcode.Conflict, "PostgreSQL role %s already exists", user)
		}
		kc, err := postgresKeychain(c.launchctl.Prefix)
		if err != nil {
			return err
		}
		kc = kc.For(user)
		pw := mysql.NewPassword()
		if err := kc.Set(ctx, pw); err != nil {
			return err
		}
		undo := func() {
			_ = c.dropPostgresDatabase(ctx, db, user)
		}
		if _, err := c.pgQuery(ctx, "postgres", "CREATE ROLE "+postgres.Ident(user)+" LOGIN PASSWORD "+postgres.Literal(pw)+";"); err != nil {
			_, _ = kc.Delete(ctx)
			return err
		}
		// CREATE DATABASE cannot run in a transaction block: its own call.
		if _, err := c.pgQuery(ctx, "postgres", "CREATE DATABASE "+postgres.Ident(db)+" OWNER "+postgres.Ident(user)+" ENCODING 'UTF8';"); err != nil {
			undo()
			return err
		}
		if _, err := c.pgQuery(ctx, "postgres", "REVOKE ALL ON DATABASE "+postgres.Ident(db)+" FROM PUBLIC;"); err != nil {
			undo()
			return err
		}
		site.Database, site.DBUser, site.DBEngine = db, user, EnginePostgres
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			undo()
			return err
		}
		res, err = c.postgresSiteDatabase(ctx, site)
		return err
	})
	return res, err
}

func (c *Core) linkPostgresDatabase(ctx context.Context, name, db string) (SiteDatabase, error) {
	var res SiteDatabase
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if site.Database != "" {
			return errcode.Newf(errcode.Conflict, "%s already uses database %s", name, site.Database)
		}
		dbs, err := c.PostgresDatabases(ctx)
		if err != nil {
			return err
		}
		if !slices.Contains(dbs, db) {
			return errcode.Newf(errcode.NotFound, "database %s does not exist", db)
		}
		site.Database, site.DBUser, site.DBEngine = db, "", EnginePostgres
		if err := c.Store.UpdateSite(ctx, site); err != nil {
			return err
		}
		res, err = c.postgresSiteDatabase(ctx, site)
		return err
	})
	return res, err
}

// dropPostgresDatabase deletes a database (closing its connections) and its
// own role and keychain item.
func (c *Core) dropPostgresDatabase(ctx context.Context, db, user string) error {
	if _, err := c.pgQuery(ctx, "postgres", "DROP DATABASE IF EXISTS "+postgres.Ident(db)+" WITH (FORCE);"); err != nil {
		return err
	}
	if user == "" {
		return nil
	}
	if _, err := c.pgQuery(ctx, "postgres", "DROP ROLE IF EXISTS "+postgres.Ident(user)+";"); err != nil {
		return err
	}
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return err
	}
	_, err = kc.For(user).Delete(ctx)
	return err
}

func (c *Core) postgresSiteDatabase(ctx context.Context, site store.Site) (SiteDatabase, error) {
	st, err := c.postgresStatus(ctx)
	if err != nil {
		return SiteDatabase{}, err
	}
	res := SiteDatabase{
		Site: site.Name, Database: site.Database, User: postgres.Superuser, Engine: EnginePostgres,
		Host: "127.0.0.1", Port: st.Port, Socket: filepath.Dir(c.Layout.Socket("x")),
	}
	kc, err := postgresKeychain(c.launchctl.Prefix)
	if err != nil {
		return res, err
	}
	if site.DBUser != "" {
		res.User, res.Dedicated = site.DBUser, true
		kc = kc.For(site.DBUser)
	}
	if res.Password, err = kc.Get(ctx); err != nil {
		return res, err
	}
	return res, nil
}
