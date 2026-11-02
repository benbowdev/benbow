// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"

	"github.com/getcovo/covo/internal/core"
	"github.com/spf13/cobra"
)

type mailResult core.MailResult

func (r mailResult) Text(w io.Writer) {
	if !r.Enabled {
		fmt.Fprintln(w, "Mail: off (PHP mail() uses the Mac's own mail system). Turn it on with `covo mail enable`.")
		return
	}
	state := "stopped: starts with `covo start`"
	if r.Running {
		state = "running"
	}
	fmt.Fprintf(w, "Mailpit %s (%s)\n  inbox  %s\n  SMTP   127.0.0.1:%d (PHP mail() goes here)\n", r.Version, state, r.URL, r.SMTPPort)
}

func (a *app) mailCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mail",
		Short: "Catch the mail PHP sends in Mailpit (status)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			st, err := c.Mail(ctx)
			return mailResult{MailStatus: st}, err
		}),
	}
	for _, on := range []bool{true, false} {
		use, short := "disable", "Stop catching mail (messages are kept)"
		if on {
			use, short = "enable", "Catch every mail PHP sends; inbox at https://mail.localhost"
		}
		cmd.AddCommand(&cobra.Command{
			Use:   use,
			Short: short,
			Args:  cobra.NoArgs,
			RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
				res, err := c.SetMail(ctx, on)
				return mailResult(res), err
			}),
		})
	}
	return cmd
}
