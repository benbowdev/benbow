// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
	"github.com/spf13/cobra"
)

type remotesResult []store.Remote

func (r remotesResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No remotes. Add one with: covo remote set <site> --kind production --host … --user … --path … --url …")
	}
	for _, x := range r {
		port := ""
		if x.Port != 0 {
			port = fmt.Sprintf(":%d", x.Port)
		}
		fmt.Fprintf(w, "%-11s %-10s %s %s@%s%s:%s  %s  (%s)\n", x.Name, x.Kind, x.Transport, x.User, x.Host, port, x.Path, x.URL, strings.Join(x.Paths, ", "))
	}
}

type syncResult core.SyncResult

func (r syncResult) Text(w io.Writer) {
	verb := "Done"
	if r.DryRun {
		verb = "Would do (nothing changed)"
	}
	fmt.Fprintf(w, "%s with %s:\n", verb, r.Remote)
	for _, s := range r.Steps {
		fmt.Fprintf(w, "  - %s\n", s)
	}
	for i, f := range r.Files {
		if i == 20 {
			fmt.Fprintf(w, "    … and %d more\n", len(r.Files)-20)
			break
		}
		fmt.Fprintf(w, "    %s\n", f)
	}
}

func (a *app) remoteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remote",
		Short: "Production and staging copies of a site (for covo pull and push)",
	}
	var r store.Remote
	var paths, excludes []string
	var askPassword, clearPassword bool
	set := &cobra.Command{
		Use:   "set <site>",
		Short: "Add or change a remote",
		Long: `A remote is a copy of the site on a server. Transports:
  ssh   your keys, ssh-agent and ~/.ssh/config (/usr/bin/ssh); files with rsync,
        database with the server's wp-cli or mysqldump
  sftp  for hosts without a shell; database through a temporary PHP bridge
  ftps / ftp  same, over FTP (plain FTP sends the password unencrypted)
A password (--password asks for it) is kept in the login keychain.`,
		Args: cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			r.Site, r.Paths, r.Excludes = args[0], paths, excludes
			var pw *string
			switch {
			case clearPassword:
				empty := ""
				pw = &empty
			case askPassword:
				p, err := a.readSecret("Password for " + r.User + "@" + r.Host + ": ")
				if err != nil {
					return nil, err
				}
				pw = &p
			}
			saved, err := c.SetRemote(ctx, r, pw)
			return remotesResult{saved}, err
		}),
	}
	set.Flags().StringVar(&r.Name, "name", "", "remote name (default: its kind)")
	set.Flags().StringVar(&r.Kind, "kind", "production", "production (pull only) or staging (pull and push)")
	set.Flags().StringVar(&r.Transport, "transport", "ssh", "ssh, sftp, ftps or ftp")
	set.Flags().StringVar(&r.Host, "host", "", "server name")
	set.Flags().IntVar(&r.Port, "port", 0, "port (default of the transport)")
	set.Flags().StringVar(&r.User, "user", "", "login")
	set.Flags().StringVar(&r.Path, "path", "", "project folder on the server")
	set.Flags().StringVar(&r.URL, "url", "", "the remote site's address")
	set.Flags().StringVar(&r.Webroot, "webroot", "", "folder served on the web, inside --path (Laravel: public)")
	set.Flags().StringSliceVar(&paths, "paths", nil, "folders to sync (default: wp-content/uploads, storage/app/public, or everything)")
	set.Flags().StringSliceVar(&excludes, "exclude", nil, "patterns never synced (default: .git, node_modules, .DS_Store, cache)")
	set.Flags().BoolVar(&askPassword, "password", false, "ask for the password and keep it in the keychain")
	set.Flags().BoolVar(&clearPassword, "no-password", false, "forget the stored password")
	cmd.AddCommand(set, &cobra.Command{
		Use:   "show <site>",
		Short: "List a site's remotes",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			l, err := c.ListRemotes(ctx, args[0])
			return remotesResult(l), err
		}),
	}, &cobra.Command{
		Use:     "remove <site> <name>",
		Aliases: []string{"rm", "delete"},
		Short:   "Forget a remote and its password",
		Args:    cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			return remotesResult{}, c.RemoveRemote(ctx, args[0], args[1])
		}),
	})
	return cmd
}

func syncFlags(cmd *cobra.Command, o *core.SyncOptions) {
	cmd.Flags().BoolVar(&o.DryRun, "dry-run", false, "show what would change, change nothing")
	cmd.Flags().BoolVar(&o.NoFiles, "no-files", false, "leave the files out")
	cmd.Flags().BoolVar(&o.NoDatabase, "no-database", false, "leave the database out")
}

func (a *app) pullCmd() *cobra.Command {
	var o core.SyncOptions
	cmd := &cobra.Command{
		Use:   "pull <site> [remote]",
		Short: "Copy a remote's database and files into the site (a snapshot is taken first)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			name := "production"
			if len(args) == 2 {
				name = args[1]
			}
			res, err := c.Pull(ctx, args[0], name, o)
			return syncResult(res), err
		}),
	}
	syncFlags(cmd, &o)
	return cmd
}

func (a *app) pushCmd() *cobra.Command {
	var o core.SyncOptions
	var yes bool
	cmd := &cobra.Command{
		Use:   "push <site> <staging remote>",
		Short: "Copy the site's database and files to a staging remote (never production)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !o.DryRun && !yes && !a.confirm("Replace "+args[1]+"'s database and files with "+args[0]+"'s?") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "pushing needs --yes")
			}
			res, err := c.Push(ctx, args[0], args[1], o)
			return syncResult(res), err
		}),
	}
	syncFlags(cmd, &o)
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask")
	return cmd
}

// askpassCmd answers ssh's password prompt from the keychain (SSH_ASKPASS).
func (a *app) askpassCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "askpass [prompt]",
		Hidden: true,
		Args:   cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			a.ran = true
			c, err := core.Open(cmd.Context())
			if err != nil {
				return err
			}
			defer func() { _ = c.Close() }()
			pw, err := c.AskPass(cmd.Context())
			if err != nil {
				return err
			}
			fmt.Fprintln(a.stdout, pw)
			return nil
		},
	}
}
