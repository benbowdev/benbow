// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

type phpCheckResult core.PHPCheckResult

func (r phpCheckResult) Text(w io.Writer) {
	for _, f := range r.Files {
		for _, m := range f.Messages {
			fmt.Fprintf(w, "%s:%d  %-7s %s\n", f.Path, m.Line, strings.ToUpper(m.Type), m.Message)
		}
	}
	what := "its code"
	if r.WordPress {
		what = strings.Join(r.Scanned, ", ")
	}
	if r.Errors == 0 && r.Warnings == 0 {
		fmt.Fprintf(w, "%s: no problems found for PHP %s in %s (%.1f s).\n", r.Site, r.Target, what, r.Seconds)
		return
	}
	fmt.Fprintf(w, "%s on PHP %s: %d errors, %d warnings in %d files (%s, %.1f s).\n",
		r.Site, r.Target, r.Errors, r.Warnings, len(r.Files), what, r.Seconds)
	fmt.Fprintln(w, "Errors break on that version; warnings are deprecated and will break later.")
}

func (a *app) phpCheckCmd() *cobra.Command {
	var target string
	var all bool
	cmd := &cobra.Command{
		Use:   "check <site>",
		Short: "List what in a site's code would break on a newer PHP version",
		Long: "Runs PHP_CodeSniffer with PHPCompatibility on the site's code, before you switch\n" +
			"its PHP version. WordPress sites: only wp-content (themes and plugins) unless --all.",
		Args: cobra.ExactArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			res, err := c.CheckPHP(ctx, args[0], target, all)
			return phpCheckResult(res), err
		}),
	}
	cmd.Flags().StringVar(&target, "target", "", "PHP version to check against (default: the newest fully supported)")
	cmd.Flags().BoolVar(&all, "all", false, "WordPress: check the core files too")
	return cmd
}
