// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/spf13/cobra"
)

type postgresResult core.PostgresResult

func (r postgresResult) Text(w io.Writer) {
	p := r.PostgresStatus
	if !p.Enabled {
		fmt.Fprintln(w, "PostgreSQL is off. Turn it on with: benbow postgres enable")
		if p.Initialized {
			fmt.Fprintf(w, "Its databases are kept in %s until benbow uninstall.\n", p.DataDir)
		}
		return
	}
	fmt.Fprintf(w, "PostgreSQL %s is %s\n  host     %s\n  port     %d\n  user     %s\n  password benbow postgres password (stored in the login keychain)\n",
		p.Version, running(p.Running), p.Host, p.Port, p.User)
	if !r.Applied && r.Changed {
		fmt.Fprintln(w, "Benbow is not running: PostgreSQL starts with `benbow start`.")
	}
}

type redisResult core.RedisResult

func (r redisResult) Text(w io.Writer) {
	s := r.RedisStatus
	if !s.Enabled {
		fmt.Fprintln(w, "Redis is off. Turn it on with: benbow redis enable")
		return
	}
	fmt.Fprintf(w, "Redis %s is %s\n  host     %s\n  port     %d\n  socket   %s\n", s.Version, running(s.Running), s.Host, s.Port, s.Socket)
	if !r.Applied && r.Changed {
		fmt.Fprintln(w, "Benbow is not running: Redis starts with `benbow start`.")
	}
}

func (a *app) postgresCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "postgres",
		Short: "Show or change Benbow's PostgreSQL server",
		Long: `Benbow can run PostgreSQL 18 on 127.0.0.1, port 5433 by default (5432 is usually
taken by Postgres.app or Homebrew). Off until enabled; databases live in
Benbow's home; the superuser (postgres) password is kept in the login keychain.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Postgres(ctx)
			return postgresResult{PostgresStatus: res}, err
		}),
	}
	var port int
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Install, initialize and start PostgreSQL",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.EnablePostgres(ctx, port)
			return postgresResult(res), err
		}),
	}
	enable.Flags().IntVar(&port, "port", 0, "TCP port on 127.0.0.1 (default: first free from 5433)")
	cmd.AddCommand(enable, &cobra.Command{
		Use:   "disable",
		Short: "Stop PostgreSQL (databases are kept)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.DisablePostgres(ctx)
			return postgresResult(res), err
		}),
	}, &cobra.Command{
		Use:   "password",
		Short: "Print the connection details, superuser password included",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.PostgresCredentials(ctx)
			return mysqlCredentialsResult(res), err
		}),
	})
	return cmd
}

func (a *app) redisCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "redis",
		Short: "Show or change Benbow's Redis server",
		Long: `Benbow can run Redis 8 on 127.0.0.1, port 6380 by default (6379 is usually taken
by another Redis). Off until enabled; no password: only this Mac reaches it.`,
		Args: cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.Redis(ctx)
			return redisResult{RedisStatus: res}, err
		}),
	}
	var port int
	enable := &cobra.Command{
		Use:   "enable",
		Short: "Install and start Redis",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.EnableRedis(ctx, port)
			return redisResult(res), err
		}),
	}
	enable.Flags().IntVar(&port, "port", 0, "TCP port on 127.0.0.1 (default: first free from 6380)")
	cmd.AddCommand(enable, &cobra.Command{
		Use:   "disable",
		Short: "Stop Redis (its saved data is kept)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.DisableRedis(ctx)
			return redisResult(res), err
		}),
	})
	return cmd
}
