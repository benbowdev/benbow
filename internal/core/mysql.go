// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/fsutil"
	"github.com/benbowdev/benbow/internal/keychain"
	"github.com/benbowdev/benbow/internal/launchd"
	"github.com/benbowdev/benbow/internal/mysql"
	"github.com/benbowdev/benbow/internal/ports"
)

const (
	mysqlService     = "mysql"
	mysqlSeries      = "8.4"
	mysqlExitTimeout = 60
)

// mysqlServer describes one of Benbow's MySQL servers: 8.4 (the default)
// or 5.7 (optional, to test old sites against the server they ran on).
type mysqlServer struct {
	engine      string // a site's database engine
	service     string // launchd service and keychain suffix
	component   string // in the manifest
	series      string
	title       string
	enabledKey  string
	portKey     string
	defaultPort int
	dir         string // under data/ and etc/
	legacy      bool   // my.cnf for 5.7
}

var (
	mysqlMain = mysqlServer{engine: EngineMySQL, service: mysqlService, component: "mysql", series: mysqlSeries, title: "MySQL",
		enabledKey: "mysql_enabled", portKey: "mysql_port", defaultPort: mysql.DefaultPort, dir: "mysql"}
	mysqlLegacy = mysqlServer{engine: EngineMySQL57, service: "mysql57", component: "mysql57", series: "5.7", title: "MySQL 5.7",
		enabledKey: "mysql57_enabled", portKey: "mysql57_port", defaultPort: 3308, dir: "mysql57", legacy: true}
)

// mysqlFor returns the server of a database engine ("" and "mysql": 8.4).
func mysqlFor(engine string) mysqlServer {
	if engine == EngineMySQL57 {
		return mysqlLegacy
	}
	return mysqlMain
}

// marker is a file every initialized data directory of this server has.
func (s mysqlServer) marker() string {
	if s.legacy {
		return filepath.Join("mysql", "user.frm") // 5.7 keeps system tables as .frm
	}
	return "mysql.ibd"
}

// cli names the server's command group.
func (s mysqlServer) cli() string { return "benbow " + s.service }

// MySQLStatus describes Benbow's MySQL server.
type MySQLStatus struct {
	// Engine is mysql (8.4) or mysql57.
	Engine  string `json:"engine"`
	Enabled bool   `json:"enabled"`
	Version string `json:"version,omitempty"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Socket  string `json:"socket,omitempty"`
	User    string `json:"user,omitempty"`
	DataDir string `json:"data_dir"`
	// Initialized is true once the data directory exists. It is kept when
	// MySQL is disabled and only deleted by uninstall.
	Initialized bool `json:"initialized"`
	// PasswordStored is true when the root password is in the keychain.
	PasswordStored bool `json:"password_stored"`
	Running        bool `json:"running"`
}

// MySQLResult reports what enable or disable did.
type MySQLResult struct {
	MySQLStatus
	Changed bool `json:"changed"`
	// Applied is false when Benbow is stopped: the server starts with `benbow start`.
	Applied bool `json:"applied"`
}

// MySQLCredentials is how to connect as root.
type MySQLCredentials struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Socket   string `json:"socket"`
	User     string `json:"user"`
	Password string `json:"password"`
}

func (c *Core) mysqlDataDirOf(s mysqlServer) string { return filepath.Join(c.Layout.Data, s.dir) }
func (c *Core) mysqlCnfPathOf(s mysqlServer) string {
	return filepath.Join(c.Layout.Etc, s.dir, "my.cnf")
}

// mysqlKeychain is where the root password lives: the login keychain, under
// a service named after the launchd prefix, so test installations never
// share it with the real one.
func mysqlKeychain(prefix string) (mysql.Keychain, error) { return mysqlKeychainOf(prefix, mysqlMain) }

func mysqlKeychainOf(prefix string, s mysqlServer) (mysql.Keychain, error) {
	p, err := keychain.LoginPath()
	if err != nil {
		return mysql.Keychain{}, err
	}
	return mysql.Keychain{Path: p, Service: prefix + "." + s.service}, nil
}

func (c *Core) mysqlEnabled(ctx context.Context) (bool, error) {
	return c.mysqlEnabledOf(ctx, mysqlMain)
}

func (c *Core) mysqlEnabledOf(ctx context.Context, s mysqlServer) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, s.enabledKey)
	return ok, err
}

// mysqlInitialized reports whether the data directory holds a server.
func (c *Core) mysqlInitialized(s mysqlServer) (bool, error) {
	dir := c.mysqlDataDirOf(s)
	if _, err := os.Stat(filepath.Join(dir, s.marker())); err == nil {
		return true, nil
	}
	if _, err := os.Lstat(dir); err == nil {
		return false, errcode.Errorf(errcode.Conflict, dir+" exists but holds no MySQL data directory; move it away and run `"+s.cli()+" enable` again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	return false, nil
}

// mysqlPort returns the stored port, assigning the first free one from 3307
// on first use. It never changes on its own: sites' configs depend on it.
func (c *Core) mysqlPort(ctx context.Context) (int, error) { return c.mysqlPortOf(ctx, mysqlMain) }

func (c *Core) mysqlPortOf(ctx context.Context, s mysqlServer) (int, error) {
	v, ok, err := c.Store.Setting(ctx, s.portKey)
	if err != nil {
		return 0, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", s.portKey, v, err)
		}
		return port, nil
	}
	// Never the other server's port, even while it is free (stopped).
	other := mysqlLegacy
	if s.legacy {
		other = mysqlMain
	}
	taken, _, _ := c.Store.Setting(ctx, other.portKey)
	start := s.defaultPort
	var port int
	for {
		if port, err = ports.FirstFree(start, portSearchSpan); err != nil {
			return 0, err
		}
		if strconv.Itoa(port) != taken {
			break
		}
		start = port + 1
	}
	return port, c.Store.SetSetting(ctx, s.portKey, strconv.Itoa(port))
}

// ensureMySQL installs the pinned MySQL server and checks it runs.
func (c *Core) ensureMySQL(ctx context.Context) (dir, version string, err error) {
	return c.ensureMySQLOf(ctx, mysqlMain)
}

func (c *Core) ensureMySQLOf(ctx context.Context, s mysqlServer) (dir, version string, err error) {
	art, err := c.pinned(ctx, s.component, s.series)
	if errcode.Code(err) == errcode.NotFound {
		if s.legacy {
			return "", "", errcode.Newf(errcode.Unsupported,
				"Benbow has no MySQL %s build for this Mac (%s) yet: it comes with a component update", s.series, runtime.GOARCH)
		}
		return "", "", errcode.Newf(errcode.Unsupported,
			"MySQL %s is only available for Apple Silicon Macs (this Mac is %s)", s.series, runtime.GOARCH)

	}
	if err != nil {
		return "", "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", "", err
	}
	bin := filepath.Join(inst.Dir, "bin", "mysqld")
	out, err := runQuiet(ctx, bin, "--no-defaults", "--version")
	if err != nil {
		return "", "", errcode.New(errcode.Service, fmt.Errorf("run %s --version: %w", bin, err))
	}
	if !strings.Contains(out, " Ver "+art.Version+" ") {
		return "", "", errcode.Newf(errcode.Checksum, "mysqld reports %q, expected %s", firstLine(out), art.Version)
	}
	return inst.Dir, art.Version, nil
}

// writeMySQLConfig writes my.cnf and returns a hash of it for the agent.
func (c *Core) writeMySQLConfig(s mysqlServer, baseDir string, port int) (string, error) {
	cnf, err := mysql.MyCnf(mysql.Config{
		BaseDir:  baseDir,
		DataDir:  c.mysqlDataDirOf(s),
		Socket:   c.mysqlSocketPathOf(s),
		PIDFile:  c.Layout.Socket(s.dir + ".pid"),
		ErrorLog: c.mysqlErrorLog(s),
		Port:     port,
		Legacy:   s.legacy,
	})
	if err != nil {
		return "", err
	}
	if _, err := fsutil.WriteAtomic(c.mysqlCnfPathOf(s), []byte(cnf), 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(cnf + "\x00" + baseDir))
	return hex.EncodeToString(sum[:8]), nil
}

// initMySQL creates the data directory with a new root password, stored in
// the keychain first so that it can never be lost. The directory is built
// next to its final place and renamed when complete.
func (c *Core) mysqlErrorLog(s mysqlServer) string {
	return filepath.Join(c.Layout.Logs, s.dir+"-error.log")
}

func (c *Core) initMySQL(ctx context.Context, s mysqlServer, baseDir string) error {
	kc, err := mysqlKeychainOf(c.launchctl.Prefix, s)
	if err != nil {
		return err
	}
	password := mysql.NewPassword()
	if err := kc.Set(ctx, password); err != nil {
		return err
	}
	stage := filepath.Join(c.Layout.Data, s.dir+".init")
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove %s: %w", stage, err)
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", stage, err)
	}
	defer os.RemoveAll(stage) // no-op after the rename
	if err := mysql.Initialize(ctx, filepath.Join(baseDir, "bin", "mysqld"), c.mysqlCnfPathOf(s), stage, password); err != nil {
		return fmt.Errorf("%w\n%s", err, tail(c.mysqlErrorLog(s), 10))
	}
	if err := os.Rename(stage, c.mysqlDataDirOf(s)); err != nil {
		return fmt.Errorf("move MySQL data directory into place: %w", err)
	}
	return nil
}

// prepareMySQL installs, configures and initializes MySQL, and returns its
// agent and port.
func (c *Core) prepareMySQL(ctx context.Context, s mysqlServer) (agent launchd.Agent, port int, err error) {
	baseDir, _, err := c.ensureMySQLOf(ctx, s)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if port, err = c.mysqlPortOf(ctx, s); err != nil {
		return launchd.Agent{}, 0, err
	}
	hash, err := c.writeMySQLConfig(s, baseDir, port)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	initialized, err := c.mysqlInitialized(s)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if !initialized {
		if err := c.initMySQL(ctx, s, baseDir); err != nil {
			return launchd.Agent{}, 0, err
		}
	} else {
		kc, err := mysqlKeychainOf(c.launchctl.Prefix, s)
		if err != nil {
			return launchd.Agent{}, 0, err
		}
		if ok, err := kc.Exists(ctx); err != nil {
			return launchd.Agent{}, 0, err
		} else if !ok {
			return launchd.Agent{}, 0, errcode.Newf(errcode.Conflict,
				"the %s data directory %s exists but its root password is not in the keychain (item %q)", s.title, c.mysqlDataDirOf(s), kc.Service)

		}
	}
	out, errLog := c.logPaths(s.service)
	return launchd.Agent{
		Label:   c.labelFor(s.service),
		Program: filepath.Join(baseDir, "bin", "mysqld"),
		Args:    []string{"--defaults-file=" + c.mysqlCnfPathOf(s)},
		// Not read by mysqld: a changed config restarts the agent.
		Env:         map[string]string{"BENBOW_CONFIG_HASH": hash},
		WorkDir:     c.Layout.Root,
		StdoutLog:   out,
		StderrLog:   errLog,
		ExitTimeout: mysqlExitTimeout,
	}, port, nil
}

// mysqlAgents returns the MySQL agent when MySQL is enabled, after checking
// that its port is free (or already Benbow's).
func (c *Core) mysqlAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	return c.mysqlAgentsOf(ctx, mysqlMain, loaded)
}

func (c *Core) mysqlAgentsOf(ctx context.Context, s mysqlServer, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	enabled, err := c.mysqlEnabledOf(ctx, s)
	if err != nil || !enabled {
		return nil, 0, err
	}
	agent, port, err := c.prepareMySQL(ctx, s)
	if err != nil {
		return nil, 0, err
	}
	if !loaded[agent.Label].Running && !ports.Free(port) {
		return nil, 0, errcode.Newf(errcode.PortInUse,
			"%s port %d is in use by %s; stop it or run `%s enable --port <free port>`",
			s.title, port, ports.Describe(ports.Owners(ctx, port)), s.cli())

	}
	return []launchd.Agent{agent}, port, nil
}

// mysqlSocketPath is where "localhost" connections go.
func (c *Core) mysqlSocketPath() string { return c.mysqlSocketPathOf(mysqlMain) }

func (c *Core) mysqlSocketPathOf(s mysqlServer) string { return c.Layout.Socket(s.dir + ".sock") }

// mysqlPHPSettings returns the socket and port php.ini points mysqli and
// PDO at, or zero values when MySQL is disabled.
func (c *Core) mysqlPHPSettings(ctx context.Context) (string, int, error) {
	enabled, err := c.mysqlEnabled(ctx)
	if err != nil || !enabled {
		return "", 0, err
	}
	port, err := c.mysqlPort(ctx)
	if err != nil {
		return "", 0, err
	}
	return c.mysqlSocketPath(), port, nil
}

func (c *Core) mysqlStatus(ctx context.Context) (MySQLStatus, error) {
	return c.mysqlStatusOf(ctx, mysqlMain)
}

func (c *Core) mysqlStatusOf(ctx context.Context, s mysqlServer) (MySQLStatus, error) {
	st := MySQLStatus{Engine: s.engine, DataDir: c.mysqlDataDirOf(s)}
	var err error
	if st.Enabled, err = c.mysqlEnabledOf(ctx, s); err != nil {
		return st, err
	}
	_, statErr := os.Stat(filepath.Join(st.DataDir, s.marker()))
	st.Initialized = statErr == nil
	kc, err := mysqlKeychainOf(c.launchctl.Prefix, s)
	if err != nil {
		return st, err
	}
	if st.PasswordStored, err = kc.Exists(ctx); err != nil {
		return st, err
	}
	if !st.Enabled {
		return st, nil
	}
	st.Version = c.pinnedVersion(ctx, s.component, s.series)
	if st.Port, err = c.mysqlPortOf(ctx, s); err != nil {
		return st, err
	}
	st.Host, st.Socket, st.User = "127.0.0.1", c.mysqlSocketPathOf(s), mysql.User
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return st, err
	}
	st.Running = loaded[c.labelFor(s.service)].Running
	return st, nil
}

// MySQL reports the state of Benbow's MySQL server.
func (c *Core) MySQL(ctx context.Context) (MySQLStatus, error) {
	return c.mysqlStatus(ctx)
}

// MySQLOf reports one of Benbow's MySQL servers by engine (mysql, mysql57).
func (c *Core) MySQLOf(ctx context.Context, engine string) (MySQLStatus, error) {
	s, err := mysqlServerOf(engine)
	if err != nil {
		return MySQLStatus{}, err
	}
	return c.mysqlStatusOf(ctx, s)
}

func mysqlServerOf(engine string) (mysqlServer, error) {
	switch engine {
	case "", EngineMySQL:
		return mysqlMain, nil
	case EngineMySQL57:
		return mysqlLegacy, nil
	}
	return mysqlServer{}, errcode.Newf(errcode.InvalidInput, "unknown MySQL server %q (mysql or mysql57)", engine)
}

// EnableMySQL installs and initializes MySQL and starts it if Benbow is
// running. port, if non-zero, changes the port first; it must be free.
func (c *Core) EnableMySQL(ctx context.Context, port int) (MySQLResult, error) {
	return c.EnableMySQLOf(ctx, EngineMySQL, port)
}

// EnableMySQLOf is EnableMySQL for one server (mysql, mysql57).
func (c *Core) EnableMySQLOf(ctx context.Context, engine string, port int) (MySQLResult, error) {
	s, err := mysqlServerOf(engine)
	if err != nil {
		return MySQLResult{}, err
	}
	var res MySQLResult
	err = c.locked(ctx, func() error {
		if port != 0 {
			if err := c.setMySQLPort(ctx, s, port); err != nil {
				return err
			}
		}
		was, err := c.mysqlEnabledOf(ctx, s)
		if err != nil {
			return err
		}
		// Install and initialize now, so that enable reports problems
		// (download, unsupported Mac) even while Benbow is stopped.
		if _, _, err := c.prepareMySQL(ctx, s); err != nil {
			return err
		}
		if err := c.Store.SetSetting(ctx, s.enabledKey, "1"); err != nil {
			return err
		}
		res.Changed = !was || port != 0
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.MySQLStatus, err = c.mysqlStatusOf(ctx, s)
		return err
	})
	return res, err
}

// DisableMySQL stops MySQL. Its data and password are kept until uninstall.
func (c *Core) DisableMySQL(ctx context.Context) (MySQLResult, error) {
	return c.DisableMySQLOf(ctx, EngineMySQL)
}

// DisableMySQLOf is DisableMySQL for one server (mysql, mysql57).
func (c *Core) DisableMySQLOf(ctx context.Context, engine string) (MySQLResult, error) {
	s, err := mysqlServerOf(engine)
	if err != nil {
		return MySQLResult{}, err
	}
	var res MySQLResult
	err = c.locked(ctx, func() error {
		was, err := c.mysqlEnabledOf(ctx, s)
		if err != nil {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, s.enabledKey); err != nil {
			return err
		}
		res.Changed = was
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.MySQLStatus, err = c.mysqlStatusOf(ctx, s)
		return err
	})
	return res, err
}

func (c *Core) setMySQLPort(ctx context.Context, s mysqlServer, port int) error {
	if port < 1024 || port > 65535 {
		return errcode.Newf(errcode.InvalidInput, "%s port must be between 1024 and 65535, got %d", s.title, port)
	}
	other := mysqlLegacy
	if s.legacy {
		other = mysqlMain
	}
	if v, ok, _ := c.Store.Setting(ctx, other.portKey); ok && v == strconv.Itoa(port) {
		return errcode.Newf(errcode.PortInUse, "port %d is %s's", port, other.title)
	}
	v, ok, err := c.Store.Setting(ctx, s.portKey)
	if err != nil {
		return err
	}
	if ok && v == strconv.Itoa(port) {
		return nil
	}
	if !ports.Free(port) {
		return errcode.Newf(errcode.PortInUse,
			"port %d is in use by %s", port, ports.Describe(ports.Owners(ctx, port)))

	}
	return c.Store.SetSetting(ctx, s.portKey, strconv.Itoa(port))
}

// MySQLCredentials returns how to connect as root, password included.
func (c *Core) MySQLCredentials(ctx context.Context) (MySQLCredentials, error) {
	return c.MySQLCredentialsOf(ctx, EngineMySQL)
}

// MySQLCredentialsOf is MySQLCredentials for one server (mysql, mysql57).
func (c *Core) MySQLCredentialsOf(ctx context.Context, engine string) (MySQLCredentials, error) {
	s, err := mysqlServerOf(engine)
	if err != nil {
		return MySQLCredentials{}, err
	}
	st, err := c.mysqlStatusOf(ctx, s)
	if err != nil {
		return MySQLCredentials{}, err
	}
	if !st.Enabled {
		return MySQLCredentials{}, errcode.Errorf(errcode.NotFound, s.title+" is not enabled: run `"+s.cli()+" enable`")
	}
	kc, err := mysqlKeychainOf(c.launchctl.Prefix, s)
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
	return MySQLCredentials{Host: st.Host, Port: st.Port, Socket: st.Socket, User: st.User, Password: pw}, nil
}
