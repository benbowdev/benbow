package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/getcovo/covo/internal/core"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/spf13/cobra"
)

func (a *app) siteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "site",
		Short: "Manage sites",
	}
	cmd.AddCommand(a.siteAddCmd(), a.siteListCmd(), a.siteRemoveCmd())
	return cmd
}

type siteResult core.SiteResult

func (r siteResult) Text(w io.Writer) {
	fmt.Fprintf(w, "%s  PHP %s  %s\n  %s\n", r.Name, r.PHPMinor, r.URL, r.Path)
	if !r.Applied {
		fmt.Fprintln(w, "Covo is not running: run `covo start`.")
	}
}

func (a *app) siteAddCmd() *cobra.Command {
	var phpVersion, path string
	var force bool
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add a site served at https://<name>.localhost",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			dir, err := absDir(path)
			if err != nil {
				return nil, err
			}
			req := core.AddSiteRequest{Name: args[0], PHP: phpVersion, Path: dir, Force: force}
			res, err := c.AddSite(ctx, req)
			if errcode.Code(err) == errcode.ConfirmationRequired && a.confirm(err.Error()+"\nAdd it anyway?") {
				req.Force = true
				res, err = c.AddSite(ctx, req)
			}
			return siteResult(res), err
		}),
	}
	cmd.Flags().StringVar(&phpVersion, "php", "8.4", "PHP version (e.g. 8.4, 8.2)")
	cmd.Flags().StringVar(&path, "path", "", "document root (default: current folder)")
	cmd.Flags().BoolVar(&force, "force", false, "add the folder even if it has no index.php or index.html")
	return cmd
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
		fmt.Fprintf(w, "%-14s PHP %-4s %-36s %s\n", s.Name, s.PHPMinor, s.URL, s.Path)
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
}

func (a *app) siteRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <name>",
		Aliases: []string{"rm"},
		Short:   "Remove a site (its folder is left untouched)",
		Args:    cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.RemoveSite(ctx, args[0])
			return siteRemoveResult(res), err
		}),
	}
}
