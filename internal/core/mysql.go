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

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/mysql"
	"github.com/getcovo/covo/internal/ports"
)

const (
	mysqlService      = "mysql"
	mysqlSeries       = "8.4"
	settingMySQL      = "mysql_enabled"
	settingMySQLPort  = "mysql_port"
	mysqlSocketName   = "mysql.sock"
	mysqlExitTimeout  = 60
	mysqlInitStageDir = "mysql.init"
)

// MySQLStatus describes Covo's MySQL server.
type MySQLStatus struct {
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
	// Applied is false when Covo is stopped: the server starts with `covo start`.
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

func (c *Core) mysqlDataDir() string { return filepath.Join(c.Layout.Data, "mysql") }
func (c *Core) mysqlCnfPath() string { return filepath.Join(c.Layout.Etc, "mysql", "my.cnf") }

// mysqlKeychain is where the root password lives: the login keychain, under
// a service named after the launchd prefix, so test installations never
// share it with the real one.
func mysqlKeychain(prefix string) (mysql.Keychain, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return mysql.Keychain{}, fmt.Errorf("resolve home directory: %w", err)
	}
	return mysql.Keychain{
		Path:    filepath.Join(home, "Library", "Keychains", "login.keychain-db"),
		Service: prefix + "." + mysqlService,
	}, nil
}

func (c *Core) mysqlEnabled(ctx context.Context) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingMySQL)
	return ok, err
}

// mysqlInitialized reports whether the data directory holds a server.
func (c *Core) mysqlInitialized() (bool, error) {
	dir := c.mysqlDataDir()
	if _, err := os.Stat(filepath.Join(dir, "mysql.ibd")); err == nil {
		return true, nil
	}
	if _, err := os.Lstat(dir); err == nil {
		return false, errcode.Errorf(errcode.Conflict, dir+" exists but holds no MySQL data directory; move it away and run `covo mysql enable` again")
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", dir, err)
	}
	return false, nil
}

// mysqlPort returns the stored port, assigning the first free one from 3307
// on first use. It never changes on its own: sites' configs depend on it.
func (c *Core) mysqlPort(ctx context.Context) (int, error) {
	v, ok, err := c.Store.Setting(ctx, settingMySQLPort)
	if err != nil {
		return 0, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", settingMySQLPort, v, err)
		}
		return port, nil
	}
	port, err := ports.FirstFree(mysql.DefaultPort, portSearchSpan)
	if err != nil {
		return 0, err
	}
	return port, c.Store.SetSetting(ctx, settingMySQLPort, strconv.Itoa(port))
}

// ensureMySQL installs the pinned MySQL server and checks it runs.
func (c *Core) ensureMySQL(ctx context.Context) (dir, version string, err error) {
	art, err := c.pinned(ctx, "mysql", mysqlSeries)
	if errcode.Code(err) == errcode.NotFound {
		return "", "", errcode.Errorf(errcode.Unsupported,
			fmt.Sprintf("MySQL %s is only available for Apple Silicon Macs (this Mac is %s)", mysqlSeries, runtime.GOARCH))
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
		return "", "", errcode.Errorf(errcode.Checksum, fmt.Sprintf("mysqld reports %q, expected %s", firstLine(out), art.Version))
	}
	return inst.Dir, art.Version, nil
}

// writeMySQLConfig writes my.cnf and returns a hash of it for the agent.
func (c *Core) writeMySQLConfig(baseDir string, port int) (string, error) {
	cnf, err := mysql.MyCnf(mysql.Config{
		BaseDir:  baseDir,
		DataDir:  c.mysqlDataDir(),
		Socket:   c.Layout.Socket(mysqlSocketName),
		PIDFile:  c.Layout.Socket("mysql.pid"),
		ErrorLog: filepath.Join(c.Layout.Logs, "mysql-error.log"),
		Port:     port,
	})
	if err != nil {
		return "", err
	}
	if _, err := fsutil.WriteAtomic(c.mysqlCnfPath(), []byte(cnf), 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(cnf + "\x00" + baseDir))
	return hex.EncodeToString(sum[:8]), nil
}

// initMySQL creates the data directory with a new root password, stored in
// the keychain first so that it can never be lost. The directory is built
// next to its final place and renamed when complete.
func (c *Core) initMySQL(ctx context.Context, baseDir string) error {
	kc, err := mysqlKeychain(c.launchctl.Prefix)
	if err != nil {
		return err
	}
	password := mysql.NewPassword()
	if err := kc.Set(ctx, password); err != nil {
		return err
	}
	stage := filepath.Join(c.Layout.Data, mysqlInitStageDir)
	if err := os.RemoveAll(stage); err != nil {
		return fmt.Errorf("remove %s: %w", stage, err)
	}
	if err := os.Mkdir(stage, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", stage, err)
	}
	defer os.RemoveAll(stage) // no-op after the rename
	if err := mysql.Initialize(ctx, filepath.Join(baseDir, "bin", "mysqld"), c.mysqlCnfPath(), stage, password); err != nil {
		return fmt.Errorf("%w\n%s", err, tail(filepath.Join(c.Layout.Logs, "mysql-error.log"), 10))
	}
	if err := os.Rename(stage, c.mysqlDataDir()); err != nil {
		return fmt.Errorf("move MySQL data directory into place: %w", err)
	}
	return nil
}

// prepareMySQL installs, configures and initializes MySQL, and returns its
// agent and port.
func (c *Core) prepareMySQL(ctx context.Context) (agent launchd.Agent, port int, err error) {
	baseDir, _, err := c.ensureMySQL(ctx)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if port, err = c.mysqlPort(ctx); err != nil {
		return launchd.Agent{}, 0, err
	}
	hash, err := c.writeMySQLConfig(baseDir, port)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	initialized, err := c.mysqlInitialized()
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if !initialized {
		if err := c.initMySQL(ctx, baseDir); err != nil {
			return launchd.Agent{}, 0, err
		}
	} else {
		kc, err := mysqlKeychain(c.launchctl.Prefix)
		if err != nil {
			return launchd.Agent{}, 0, err
		}
		if ok, err := kc.Exists(ctx); err != nil {
			return launchd.Agent{}, 0, err
		} else if !ok {
			return launchd.Agent{}, 0, errcode.Errorf(errcode.Conflict, fmt.Sprintf(
				"the MySQL data directory %s exists but its root password is not in the keychain (item %q)", c.mysqlDataDir(), kc.Service))
		}
	}
	out, errLog := c.logPaths(mysqlService)
	return launchd.Agent{
		Label:   c.labelFor(mysqlService),
		Program: filepath.Join(baseDir, "bin", "mysqld"),
		Args:    []string{"--defaults-file=" + c.mysqlCnfPath()},
		// Not read by mysqld: a changed config restarts the agent.
		Env:         map[string]string{"COVO_CONFIG_HASH": hash},
		WorkDir:     c.Layout.Root,
		StdoutLog:   out,
		StderrLog:   errLog,
		ExitTimeout: mysqlExitTimeout,
	}, port, nil
}

// mysqlAgents returns the MySQL agent when MySQL is enabled, after checking
// that its port is free (or already Covo's).
func (c *Core) mysqlAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	enabled, err := c.mysqlEnabled(ctx)
	if err != nil || !enabled {
		return nil, 0, err
	}
	agent, port, err := c.prepareMySQL(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !loaded[agent.Label].Running && !ports.Free(port) {
		return nil, 0, errcode.Errorf(errcode.PortInUse, fmt.Sprintf(
			"MySQL port %d is in use by %s; stop it or run `covo mysql enable --port <free port>`",
			port, ports.Describe(ports.Owners(ctx, port))))
	}
	return []launchd.Agent{agent}, port, nil
}

// mysqlSocketPath is where "localhost" connections go.
func (c *Core) mysqlSocketPath() string { return c.Layout.Socket(mysqlSocketName) }

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
	st := MySQLStatus{DataDir: c.mysqlDataDir()}
	var err error
	if st.Enabled, err = c.mysqlEnabled(ctx); err != nil {
		return st, err
	}
	_, statErr := os.Stat(filepath.Join(st.DataDir, "mysql.ibd"))
	st.Initialized = statErr == nil
	kc, err := mysqlKeychain(c.launchctl.Prefix)
	if err != nil {
		return st, err
	}
	if st.PasswordStored, err = kc.Exists(ctx); err != nil {
		return st, err
	}
	if !st.Enabled {
		return st, nil
	}
	st.Version = c.pinnedVersion(ctx, "mysql", mysqlSeries)
	if st.Port, err = c.mysqlPort(ctx); err != nil {
		return st, err
	}
	st.Host, st.Socket, st.User = "127.0.0.1", c.mysqlSocketPath(), mysql.User
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return st, err
	}
	st.Running = loaded[c.labelFor(mysqlService)].Running
	return st, nil
}

// MySQL reports the state of Covo's MySQL server.
func (c *Core) MySQL(ctx context.Context) (MySQLStatus, error) {
	return c.mysqlStatus(ctx)
}

// EnableMySQL installs and initializes MySQL and starts it if Covo is
// running. port, if non-zero, changes the port first; it must be free.
func (c *Core) EnableMySQL(ctx context.Context, port int) (MySQLResult, error) {
	var res MySQLResult
	err := c.locked(ctx, func() error {
		if port != 0 {
			if err := c.setMySQLPort(ctx, port); err != nil {
				return err
			}
		}
		was, err := c.mysqlEnabled(ctx)
		if err != nil {
			return err
		}
		// Install and initialize now, so that enable reports problems
		// (download, unsupported Mac) even while Covo is stopped.
		if _, _, err := c.prepareMySQL(ctx); err != nil {
			return err
		}
		if err := c.Store.SetSetting(ctx, settingMySQL, "1"); err != nil {
			return err
		}
		res.Changed = !was || port != 0
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.MySQLStatus, err = c.mysqlStatus(ctx)
		return err
	})
	return res, err
}

// DisableMySQL stops MySQL. Its data and password are kept until uninstall.
func (c *Core) DisableMySQL(ctx context.Context) (MySQLResult, error) {
	var res MySQLResult
	err := c.locked(ctx, func() error {
		was, err := c.mysqlEnabled(ctx)
		if err != nil {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingMySQL); err != nil {
			return err
		}
		res.Changed = was
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.MySQLStatus, err = c.mysqlStatus(ctx)
		return err
	})
	return res, err
}

func (c *Core) setMySQLPort(ctx context.Context, port int) error {
	if port < 1024 || port > 65535 {
		return errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("MySQL port must be between 1024 and 65535, got %d", port))
	}
	v, ok, err := c.Store.Setting(ctx, settingMySQLPort)
	if err != nil {
		return err
	}
	if ok && v == strconv.Itoa(port) {
		return nil
	}
	if !ports.Free(port) {
		return errcode.Errorf(errcode.PortInUse,
			fmt.Sprintf("port %d is in use by %s", port, ports.Describe(ports.Owners(ctx, port))))
	}
	return c.Store.SetSetting(ctx, settingMySQLPort, strconv.Itoa(port))
}

// MySQLCredentials returns how to connect as root, password included.
func (c *Core) MySQLCredentials(ctx context.Context) (MySQLCredentials, error) {
	st, err := c.mysqlStatus(ctx)
	if err != nil {
		return MySQLCredentials{}, err
	}
	if !st.Enabled {
		return MySQLCredentials{}, errcode.Errorf(errcode.NotFound, "MySQL is not enabled: run `covo mysql enable`")
	}
	kc, err := mysqlKeychain(c.launchctl.Prefix)
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
