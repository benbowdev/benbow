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
	"github.com/spf13/cobra"
)

type uninstallResult core.UninstallResult

func (r uninstallResult) Text(w io.Writer) {
	if !r.Done {
		return
	}
	if len(r.Agents)+len(r.CA)+len(r.Resolver)+len(r.Paths) == 0 {
		fmt.Fprintln(w, "Nothing to remove: Covo is not installed.")
		return
	}
	writePlan(w, r.UninstallPlan, "Removed")
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
	for _, path := range p.Paths {
		fmt.Fprintf(w, "%s %s\n", verb, path)
	}
}

func (a *app) uninstallCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Stop Covo, remove its CA and delete all its data",
		Long: `Stops every Covo service, removes Covo's CA from the login keychain and
deletes ~/Library/Application Support/covo. Your site folders are never touched.`,
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
				if len(plan.Agents)+len(plan.CA)+len(plan.Resolver)+len(plan.Paths) > 0 &&
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
