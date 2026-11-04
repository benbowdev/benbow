// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) siteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Manage sites",
	}
	cmd.AddCommand(a.siteAddCmd(), a.siteSetCmd(), a.siteToggleCmd(true), a.siteToggleCmd(false), a.siteArchiveCmd(true), a.siteArchiveCmd(false), a.siteLogsCmd(), a.sitePHPCmd(), a.siteListCmd(), a.siteRemoveCmd())
	return cmd
}

type siteResult core.SiteResult

func (r siteResult) Text(w io.Writer) {
	fmt.Fprintf(w, "%s  PHP %s  %s\n  %s\n", r.Name, r.PHPMinor, r.URL, r.Path)
	if len(r.Aliases) > 0 {
		fmt.Fprintf(w, "  also: %s\n", strings.Join(r.Aliases, ", "))
	}
	if r.Warning != "" {
		fmt.Fprintf(w, "Warning: %s.\n", r.Warning)
	}
	switch {
	case r.Archived:
		fmt.Fprintln(w, "Archived: not served. Bring it back with `covo site unarchive`.")
	case !r.Enabled:
		fmt.Fprintln(w, "Disabled: Covo does not serve it. Turn it on with `covo site enable`.")
	case !r.Applied:
		fmt.Fprintln(w, "Covo is not running: run `covo start`.")
	}
}

func (a *app) siteAddCmd() *cobra.Command {
	var req core.AddSiteRequest
	var path, group, database, engine string
	var ini []string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a site served at https://<name>.localhost (or --domain)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req.Name = args[0]
			var err error
			if req.Path, err = absDir(path); err != nil {
				return nil, err
			}
			if req.Group, err = c.ResolveGroup(ctx, group); err != nil {
				return nil, err
			}
			if req.PHPSettings, err = iniFlags(ini); err != nil {
				return nil, err
			}
			req.DatabaseEngine = engine
			switch database {
			case "":
			case "new":
				req.NewDatabase = true
			default:
				req.LinkDatabase = database
			}
			res, err := c.AddSite(ctx, req)
			if errcode.Code(err) == errcode.ConfirmationRequired && a.confirm(err.Error()+"\nAdd it anyway?") {
				req.Force = true
				res, err = c.AddSite(ctx, req)
			}
			return siteResult(res), err
		}),
	}
	cmd.Flags().StringVar(&req.PHP, "php", "8.4", "PHP version (e.g. 8.4, 8.2)")
	cmd.Flags().StringVar(&path, "path", "", "document root (default: current folder)")
	cmd.Flags().StringVar(&req.Domain, "domain", "", "local domain, e.g. demo.covo.test (default: <name>.localhost)")
	cmd.Flags().BoolVar(&req.Force, "force", false, "add the folder even if it has no index.php or index.html")
	cmd.Flags().BoolVar(&req.CreateFolder, "create-folder", false, "create the document root (missing or empty) for a new site")
	cmd.Flags().StringSliceVar(&req.Aliases, "aliases", nil, "extra domains, comma separated")
	cmd.Flags().StringVar(&group, "group", "", "list the site in this group, e.g. Clients/Acme (see covo group)")
	cmd.Flags().StringVar(&database, "database", "", `"new" for a database with its own user, or the name of an existing one to link`)
	cmd.Flags().StringArrayVar(&ini, "ini", nil, "php.ini value of this site, name=value (repeatable)")
	cmd.Flags().StringVar(&engine, "engine", "mysql", "database engine for --database: mysql or pgsql")
	return cmd
}

// iniFlags parses repeated name=value flags.
func iniFlags(flags []string) (map[string]string, error) {
	if len(flags) == 0 {
		return nil, nil
	}
	out := map[string]string{}
	for _, kv := range flags {
		name, value, ok := strings.Cut(kv, "=")
		if !ok {
			return nil, errcode.Newf(errcode.InvalidInput, "%q: want name=value", kv)
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(value)
	}
	return out, nil
}

func (a *app) siteSetCmd() *cobra.Command {
	var phpVersion, path, domain, group, title string
	var aliases []string
	var force bool
	var cmd *cobra.Command
	cmd = &cobra.Command{
		Use:   "set <name>",
		Short: "Change a site's PHP version, folder, domain, aliases, group or title",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req := core.UpdateSiteRequest{Name: args[0], PHP: phpVersion, Domain: domain, Force: force}
			if cmd.Flags().Changed("aliases") {
				req.Aliases = &aliases
			}
			if cmd.Flags().Changed("title") {
				req.Title = &title
			}
			if cmd.Flags().Changed("group") {
				id, err := c.ResolveGroup(ctx, group)
				if err != nil {
					return nil, err
				}
				req.Group = &id
			}
			if path != "" {
				dir, err := absDir(path)
				if err != nil {
					return nil, err
				}
				req.Path = dir
			}
			res, err := c.UpdateSite(ctx, req)
			if errcode.Code(err) == errcode.ConfirmationRequired && a.confirm(err.Error()+"\nUse it anyway?") {
				req.Force = true
				res, err = c.UpdateSite(ctx, req)
			}
			return siteResult(res), err
		}),
	}
	cmd.Flags().StringVar(&phpVersion, "php", "", "new PHP version (e.g. 8.3)")
	cmd.Flags().StringVar(&path, "path", "", "new document root")
	cmd.Flags().StringVar(&domain, "domain", "", "new local domain, e.g. demo.covo.test")
	cmd.Flags().StringSliceVar(&aliases, "aliases", nil, `extra domains, comma separated, replacing the current ones ("" clears)`)
	cmd.Flags().StringVar(&group, "group", "", `move the site into a group, e.g. Clients/Acme ("" for the top level)`)
	cmd.Flags().StringVar(&title, "title", "", `label shown in the list instead of the name ("" clears)`)
	cmd.Flags().BoolVar(&force, "force", false, "accept a folder without index.php or index.html")
	return cmd
}

func (a *app) siteToggleCmd(on bool) *cobra.Command {
	use, short := "disable <name>", "Stop serving a site, keeping it in the list"
	if on {
		use, short = "enable <name>", "Serve a site that was disabled"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.UpdateSite(ctx, core.UpdateSiteRequest{Name: args[0], Enabled: &on})
			return siteResult(res), err
		}),
	}
}

type siteLogResult core.SiteLog

func (r siteLogResult) Text(w io.Writer) {
	for _, l := range r.Lines {
		fmt.Fprintln(w, l)
	}
}

func (a *app) siteLogsCmd() *cobra.Command {
	var php, empty bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs <name>",
		Short: "Show the end of a site's access log (or --php for PHP errors)",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if empty {
				res, err := c.ClearSiteLogs(ctx, args[0])
				return siteLogResult(res), err
			}
			kind := core.LogAccess
			if php {
				kind = core.LogPHP
			}
			res, err := c.SiteLogs(ctx, args[0], kind, lines)
			return siteLogResult(res), err
		}),
	}
	cmd.Flags().BoolVar(&php, "php", false, "PHP errors, warnings and notices instead of requests")
	cmd.Flags().IntVarP(&lines, "lines", "n", 50, "number of lines (max 2000)")
	cmd.Flags().BoolVar(&empty, "clear", false, "empty both logs of the site")
	return cmd
}

type sitePHPResult core.SitePHPSettings

func (r sitePHPResult) Text(w io.Writer) {
	own := func(name string) string {
		if _, ok := r.Overrides[name]; ok {
			return "  (this site)"
		}
		return "  (PHP " + r.PHP + ")"
	}
	onOff := map[bool]string{true: "On", false: "Off"}
	fmt.Fprintf(w, "%s\n  memory_limit         %-6s%s\n  upload_max_filesize  %-6s%s\n  max_execution_time   %-6d%s\n  display_errors       %-6s%s\n",
		r.Site, r.MemoryLimit, own("memory_limit"), r.UploadMaxFilesize, own("upload_max_filesize"),
		r.MaxExecutionTime, own("max_execution_time"), onOff[r.DisplayErrors], own("display_errors"))
}

func (a *app) sitePHPCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "php <name> [setting=value...]",
		Short: "Show or change php.ini settings of one site (setting= goes back to the PHP version's)",
		Args:  cobra.MinimumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if len(args) == 1 {
				res, err := c.GetSitePHPSettings(ctx, args[0])
				return sitePHPResult(res), err
			}
			values := map[string]string{}
			for _, kv := range args[1:] {
				name, value, ok := strings.Cut(kv, "=")
				if !ok {
					return nil, errcode.Newf(errcode.InvalidInput, "%q: want name=value", kv)
				}
				values[name] = value
			}
			res, err := c.SetSitePHPSettings(ctx, args[0], values)
			return sitePHPResult(res), err
		}),
	}
}

func (a *app) siteArchiveCmd(archive bool) *cobra.Command {
	use, short := "unarchive <name>", "Bring back an archived site and serve it again"
	if archive {
		use, short = "archive <name>", "Put a site away: not served, listed apart (folder and database are kept)"
	}
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.UpdateSite(ctx, core.UpdateSiteRequest{Name: args[0], Archived: &archive})
			return siteResult(res), err
		}),
	}
}

func absDir(path string) (string, error) {
	if path == "" {
		wd, err := os.Getwd()
		if err != nil {
			return "", errcode.New(errcode.InvalidInput, fmt.Errorf("current folder: %w", err))
		}
		return wd, nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", errcode.New(errcode.InvalidInput, fmt.Errorf("path %q: %w", path, err))
	}
	return abs, nil
}

type siteListResult []core.SiteStatus

func (r siteListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No sites. Add one with: covo site add <name> --php 8.4")
	}
	for _, s := range r {
		off := ""
		switch {
		case s.Archived:
			off = "  (archived)"
		case !s.Enabled:
			off = "  (disabled)"
		}
		fmt.Fprintf(w, "%-14s PHP %-4s %-36s %s%s\n", s.Name, s.PHPMinor, s.URL, s.Path, off)
	}
}

func (a *app) siteListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List sites",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			res, err := c.ListSites(ctx)
			return siteListResult(res), err
		}),
	}
}

type siteRemoveResult core.SiteResult

func (r siteRemoveResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Removed %s (folder %s left untouched)\n", r.Name, r.Path)
	if r.Database != "" {
		fmt.Fprintf(w, "Database %s kept (remove with --drop-database deletes it too).\n", r.Database)
	}
}

func (a *app) siteRemoveCmd() *cobra.Command {
	var dropDB, yes bool
	cmd := &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a site (its folder is left untouched)",
		Args:    cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !dropDB {
				res, err := c.RemoveSite(ctx, args[0])
				return siteRemoveResult(res), err
			}
			if !yes && !a.confirm("Remove "+args[0]+" and delete its database with all its data? This cannot be undone.") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "deleting a database needs --yes")
			}
			res, err := c.RemoveSiteAndDatabase(ctx, args[0])
			return siteRemoveResult(res), err
		}),
	}
	cmd.Flags().BoolVar(&dropDB, "drop-database", false, "also delete the site's database and its own MySQL user")
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask before deleting the database")
	return cmd
}
