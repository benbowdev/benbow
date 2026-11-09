// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/store"
	"github.com/spf13/cobra"
)

type blueprintListResult []core.BlueprintInfo

func (r blueprintListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No blueprints. Save one from a site with: benbow blueprint save <site> <name>")
	}
	for _, b := range r {
		fmt.Fprintf(w, "%-24s %d themes, %d plugins  %s\n", b.Name, b.Themes, b.Plugins, b.Description)
	}
}

type blueprintInfoResult struct {
	core.BlueprintInfo
	Warnings []string `json:"warnings,omitempty"`
}

func (r blueprintInfoResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Blueprint %s saved: %d themes, %d plugins\n", r.Name, r.Themes, r.Plugins)
	for _, warn := range r.Warnings {
		fmt.Fprintln(w, "  warning: "+warn)
	}
}

// blueprintJSON prints a blueprint as its JSON file.
type blueprintJSON core.Blueprint

func (r blueprintJSON) Text(w io.Writer) {
	data, _ := json.MarshalIndent(core.Blueprint(r), "", "  ")
	fmt.Fprintln(w, string(data))
}

type blueprintApplyResult core.BlueprintResult

func (r blueprintApplyResult) Text(w io.Writer) {
	fmt.Fprintf(w, "Blueprint %s:\n", r.Blueprint)
	if len(r.Installed) == 0 {
		fmt.Fprintln(w, "  nothing to install")
	}
	for _, i := range r.Installed {
		fmt.Fprintln(w, "  "+i)
	}
	for _, warn := range r.Warnings {
		fmt.Fprintln(w, "  warning: "+warn)
	}
	if r.Snapshot != "" {
		fmt.Fprintf(w, "Snapshot %s has the site as it was before.\n", r.Snapshot)
	}
}

type blueprintSourcesResult []store.BlueprintSource

func (r blueprintSourcesResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No downloads recorded yet.")
	}
	for _, s := range r {
		fmt.Fprintf(w, "%s  %s  first used %s\n", s.SHA256[:12], s.URL, s.FirstUsed.Local().Format("2006-01-02"))
	}
}

type githubTokenResult struct {
	Stored bool `json:"stored"`
}

func (r githubTokenResult) Text(w io.Writer) {
	if r.Stored {
		fmt.Fprintln(w, "GitHub token stored in the login keychain.")
	} else {
		fmt.Fprintln(w, "No GitHub token stored.")
	}
}

func readBlueprintFile(name string, stdin io.Reader) ([]byte, error) {
	r := stdin
	if name != "-" {
		f, err := os.Open(name) //nolint:gosec // the user's file
		if err != nil {
			return nil, errcode.New(errcode.NotFound, fmt.Errorf("open %s: %w", name, err))
		}
		defer f.Close()
		r = f
	}
	if r == nil {
		r = os.Stdin
	}
	data, err := io.ReadAll(io.LimitReader(r, 4<<20))
	if err != nil {
		return nil, errcode.New(errcode.InvalidInput, fmt.Errorf("read %s: %w", name, err))
	}
	return data, nil
}

func (a *app) blueprintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "blueprint",
		Short: "WordPress site templates: themes, plugins, options",
		Long: "Blueprints are JSON templates for new WordPress sites: WordPress version and\n" +
			"language, themes and plugins (wordpress.org, zip URL, GitHub or git), options,\n" +
			"wp-config constants and must-use plugins. See docs/blueprints.md.\n\n" + core.BlueprintNotice,
	}
	var force bool
	save := &cobra.Command{
		Use:   "save <site> <name>",
		Short: "Save a WordPress site's themes, plugins and settings as a blueprint",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			bp, warnings, err := c.BlueprintFromSite(ctx, args[0], args[1])
			if err != nil {
				return nil, err
			}
			info, err := c.SaveBlueprint(ctx, bp, force)
			return blueprintInfoResult{info, warnings}, err
		}),
	}
	save.Flags().BoolVar(&force, "force", false, "replace a blueprint with the same name")
	var name string
	imp := &cobra.Command{
		Use:   "import <file.json|->",
		Short: "Add a blueprint from a JSON file",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			data, err := readBlueprintFile(args[0], a.stdin)
			if err != nil {
				return nil, err
			}
			if name != "" {
				var raw map[string]any
				if json.Unmarshal(data, &raw) == nil {
					raw["name"] = name
					data, _ = json.Marshal(raw)
				}
			}
			bp, err := core.ParseBlueprint(data)
			if err != nil {
				return nil, err
			}
			info, err := c.SaveBlueprint(ctx, bp, force)
			return blueprintInfoResult{BlueprintInfo: info}, err
		}),
	}
	imp.Flags().StringVar(&name, "name", "", "save under this name instead of the file's")
	imp.Flags().BoolVar(&force, "force", false, "replace a blueprint with the same name")
	export := &cobra.Command{
		Use:   "export <name> [file.json]",
		Short: "Write a blueprint as JSON (to stdout without a file)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			bp, err := c.GetBlueprint(ctx, args[0])
			if err != nil || len(args) == 1 {
				return blueprintJSON(bp), err
			}
			data, _ := json.MarshalIndent(bp, "", "  ")
			if err := os.WriteFile(args[1], append(data, '\n'), 0o600); err != nil {
				return nil, fmt.Errorf("write %s: %w", args[1], err)
			}
			return blueprintJSON(bp), nil
		}),
	}
	var opts core.ApplyOptions
	var yes bool
	apply := &cobra.Command{
		Use:   "apply <name> <site>",
		Short: "Install a blueprint's themes, plugins and settings into a WordPress site (snapshot first)",
		Args:  cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			if !opts.DryRun && !yes && !a.confirm(core.BlueprintNotice+"\nApply blueprint "+args[0]+" to "+args[1]+"?") {
				return nil, errcode.Errorf(errcode.ConfirmationRequired, "applying a blueprint needs --yes")
			}
			r, err := c.ApplyBlueprint(ctx, args[1], args[0], opts)
			return blueprintApplyResult(r), err
		}),
	}
	apply.Flags().BoolVar(&yes, "yes", false, "do not ask")
	apply.Flags().BoolVar(&opts.DryRun, "dry-run", false, "only show what would be installed")
	apply.Flags().BoolVar(&opts.AcceptChanges, "accept-changes", false, "accept sources whose content changed since first use")
	var del bool
	token := &cobra.Command{
		Use:   "github-token",
		Short: "Store a GitHub token for private repositories (read from the terminal or stdin)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			if del {
				err := c.SetGitHubToken(ctx, "")
				return githubTokenResult{}, err
			}
			t, err := a.readSecret("GitHub token: ")
			if err != nil {
				return nil, err
			}
			if strings.TrimSpace(t) == "" {
				return nil, errcode.Errorf(errcode.InvalidInput, "no token given (use --delete to remove it)")
			}
			if err := c.SetGitHubToken(ctx, t); err != nil {
				return nil, err
			}
			return githubTokenResult{Stored: true}, nil
		}),
	}
	token.Flags().BoolVar(&del, "delete", false, "remove the stored token")
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List blueprints",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			l, err := c.ListBlueprints(ctx)
			return blueprintListResult(l), err
		}),
	}, &cobra.Command{
		Use:   "show <name>",
		Short: "Show a blueprint's JSON",
		Args:  cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			bp, err := c.GetBlueprint(ctx, args[0])
			return blueprintJSON(bp), err
		}),
	}, save, imp, export, apply, &cobra.Command{
		Use:     "delete <name>",
		Aliases: []string{"rm", "remove"},
		Short:   "Delete a blueprint",
		Args:    cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			return blueprintListResult{}, c.DeleteBlueprint(ctx, args[0])
		}),
	}, &cobra.Command{
		Use:   "sources",
		Short: "List the downloads Benbow recorded, with their SHA-256",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			l, err := c.BlueprintSources(ctx)
			return blueprintSourcesResult(l), err
		}),
	}, token)
	return cmd
}
