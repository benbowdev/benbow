// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/spf13/cobra"
)

type uninstallResult core.UninstallResult

func (r uninstallResult) Text(w io.Writer) {
	if !r.Done {
		return
	}
	if len(r.Agents)+len(r.CA)+len(r.Resolver)+len(r.HostsHelper)+len(r.Keychain)+len(r.Paths) == 0 {
		fmt.Fprintln(w, "Nothing to remove: Benbow is not installed.")
		return
	}
	writePlan(w, r.UninstallPlan, "Removed")
	for _, warn := range r.Warnings {
		fmt.Fprintln(w, "Warning: "+warn)
	}
	fmt.Fprintln(w, "Your site folders were not touched.")
}

func writePlan(w io.Writer, p core.UninstallPlan, verb string) {
	for _, a := range p.Agents {
		fmt.Fprintf(w, "%s launchd agent %s\n", verb, a)
	}
	for _, c := range p.CA {
		fmt.Fprintf(w, "%s %s (SHA-1 %s) from the login keychain\n", verb, c.CommonName, c.SHA1)
	}
	for _, path := range p.Resolver {
		fmt.Fprintf(w, "%s %s (administrator authorization)\n", verb, path)
	}
	for _, item := range p.HostsHelper {
		fmt.Fprintf(w, "%s %s\n", verb, item)
	}
	for _, item := range p.Keychain {
		fmt.Fprintf(w, "%s %s from the login keychain\n", verb, item)
	}
	for _, a := range p.AgentConfigs {
		fmt.Fprintf(w, "%s Benbow's MCP server from %s (%s)\n", verb, a.Name, a.Path)
	}
	for _, path := range p.Paths {
		fmt.Fprintf(w, "%s %s\n", verb, path)
	}
	if len(p.MigrationBackups) > 0 && verb != "Removed" {
		fmt.Fprintln(w, "The backups of wp-config.php and .env taken before migrations go with it; copy them first if you may still need them:")
		for _, b := range p.MigrationBackups {
			fmt.Fprintln(w, "  "+b)
		}
	}
}

func (a *app) uninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop Benbow, remove its CA and delete all its data",
		Long: `Stops every Benbow service, removes Benbow's CA and every secret Benbow keeps in
the login keychain (MySQL and PostgreSQL passwords, passwords of production
and staging servers, the GitHub token for blueprints) and deletes
~/Library/Application Support/benbow, databases included. Your site folders
are never touched.`,
		Args: cobra.NoArgs,
		// Not withCore: opening the core would recreate the folders being removed.
		RunE: a.run(func(ctx context.Context, _ []string) (Result, error) {
			u, err := core.NewUninstaller()
			if err != nil {
				return nil, err
			}
			if !yes {
				plan, err := u.Plan(ctx)
				if err != nil {
					return nil, err
				}
				var b strings.Builder
				writePlan(&b, plan, "Will remove")
				if len(plan.Agents)+len(plan.CA)+len(plan.Resolver)+len(plan.HostsHelper)+len(plan.Keychain)+len(plan.Paths) > 0 &&
					!a.confirm(b.String()+"Your site folders will not be touched. Continue?") {
					return nil, errcode.Errorf(errcode.ConfirmationRequired, "uninstall not confirmed; pass --yes to proceed")
				}
			}
			res, err := u.Uninstall(ctx)
			return uninstallResult(res), err
		}),
	}
	cmd.Flags().BoolVar(&yes, "yes", false, "do not ask for confirmation")
	return cmd
}
