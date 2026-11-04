// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/ports"
)

// Redis: optional, off by default, one server for every site on
// 127.0.0.1 (no password: nothing outside this Mac reaches it), data saved
// under the Covo folder.

const (
	redisService      = "redis"
	redisSeries       = "8"
	settingRedis      = "redis_enabled"
	settingRedisPort  = "redis_port"
	redisDefaultPort  = 6380
	redisSocketName   = "redis.sock"
	redisSaveInterval = "3600 1 300 100"
)

// RedisStatus describes Covo's Redis server.
type RedisStatus struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version,omitempty"`
	Host    string `json:"host,omitempty"`
	Port    int    `json:"port,omitempty"`
	Socket  string `json:"socket,omitempty"`
	DataDir string `json:"data_dir"`
	Running bool   `json:"running"`
}

// RedisResult reports what enable or disable did.
type RedisResult struct {
	RedisStatus
	Changed bool `json:"changed"`
	Applied bool `json:"applied"`
}

func (c *Core) redisDataDir() string { return filepath.Join(c.Layout.Data, "redis") }

func (c *Core) redisEnabled(ctx context.Context) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingRedis)
	return ok, err
}

func (c *Core) redisPort(ctx context.Context) (int, error) {
	return c.storedPort(ctx, settingRedisPort, redisDefaultPort)
}

func (c *Core) ensureRedis(ctx context.Context) (dir string, err error) {
	art, err := c.pinned(ctx, "redis", redisSeries)
	if errcode.Code(err) == errcode.NotFound {
		return "", errcode.Errorf(errcode.Unsupported, "Redis is not available yet: run `covo update check` to fetch the newest manifest")
	}
	if err != nil {
		return "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", err
	}
	bin := filepath.Join(inst.Dir, "redis-server")
	out, err := runQuiet(ctx, bin, "--version")
	if err != nil {
		return "", errcode.New(errcode.Service, fmt.Errorf("run %s --version: %w", bin, err))
	}
	if !strings.Contains(out, "v="+art.Version+" ") {
		return "", errcode.Newf(errcode.Checksum, "redis-server reports %q, expected %s", firstLine(out), art.Version)
	}
	return inst.Dir, nil
}

func (c *Core) prepareRedis(ctx context.Context) (launchd.Agent, int, error) {
	dir, err := c.ensureRedis(ctx)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	port, err := c.redisPort(ctx)
	if err != nil {
		return launchd.Agent{}, 0, err
	}
	if err := os.MkdirAll(c.redisDataDir(), 0o700); err != nil {
		return launchd.Agent{}, 0, fmt.Errorf("create %s: %w", c.redisDataDir(), err)
	}
	out, errLog := c.logPaths(redisService)
	return launchd.Agent{
		Label:   c.labelFor(redisService),
		Program: filepath.Join(dir, "redis-server"),
		Args: []string{
			"--port", strconv.Itoa(port), "--bind", "127.0.0.1", "--protected-mode", "yes",
			"--unixsocket", c.Layout.Socket(redisSocketName), "--unixsocketperm", "700",
			"--dir", c.redisDataDir(), "--dbfilename", "dump.rdb", "--save", redisSaveInterval,
			"--appendonly", "no", "--daemonize", "no", "--logfile", "",
		},
		WorkDir:   c.Layout.Root,
		StdoutLog: out,
		StderrLog: errLog,
	}, port, nil
}

func (c *Core) redisAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, error) {
	enabled, err := c.redisEnabled(ctx)
	if err != nil || !enabled {
		return nil, 0, err
	}
	agent, port, err := c.prepareRedis(ctx)
	if err != nil {
		return nil, 0, err
	}
	if !loaded[agent.Label].Running && !ports.Free(port) {
		return nil, 0, errcode.Newf(errcode.PortInUse,
			"Redis port %d is in use by %s; stop it or run `covo redis enable --port <free port>`",
			port, ports.Describe(ports.Owners(ctx, port)))

	}
	return []launchd.Agent{agent}, port, nil
}

func (c *Core) redisStatus(ctx context.Context) (RedisStatus, error) {
	st := RedisStatus{DataDir: c.redisDataDir()}
	var err error
	if st.Enabled, err = c.redisEnabled(ctx); err != nil || !st.Enabled {
		return st, err
	}
	st.Version = c.pinnedVersion(ctx, "redis", redisSeries)
	if st.Port, err = c.redisPort(ctx); err != nil {
		return st, err
	}
	st.Host, st.Socket = "127.0.0.1", c.Layout.Socket(redisSocketName)
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return st, err
	}
	st.Running = loaded[c.labelFor(redisService)].Running
	return st, nil
}

// Redis reports the state of Covo's Redis server.
func (c *Core) Redis(ctx context.Context) (RedisStatus, error) { return c.redisStatus(ctx) }

// EnableRedis installs Redis and starts it if Covo is running.
func (c *Core) EnableRedis(ctx context.Context, port int) (RedisResult, error) {
	var res RedisResult
	err := c.locked(ctx, func() error {
		if port != 0 {
			if err := c.setStoredPort(ctx, settingRedisPort, "Redis", port); err != nil {
				return err
			}
		}
		was, err := c.redisEnabled(ctx)
		if err != nil {
			return err
		}
		if _, _, err := c.prepareRedis(ctx); err != nil {
			return err
		}
		if err := c.Store.SetSetting(ctx, settingRedis, "1"); err != nil {
			return err
		}
		res.Changed = !was || port != 0
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.RedisStatus, err = c.redisStatus(ctx)
		return err
	})
	return res, err
}

// DisableRedis stops Redis; its saved data is kept.
func (c *Core) DisableRedis(ctx context.Context) (RedisResult, error) {
	var res RedisResult
	err := c.locked(ctx, func() error {
		was, err := c.redisEnabled(ctx)
		if err != nil {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingRedis); err != nil {
			return err
		}
		res.Changed = was
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		res.RedisStatus, err = c.redisStatus(ctx)
		return err
	})
	return res, err
}
