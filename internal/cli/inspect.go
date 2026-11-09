// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/benbowdev/benbow/internal/core"
	"github.com/spf13/cobra"
)

// Tools to check a site's work: fetch a page with its PHP errors, read the
// mail PHP sent, query the database read-only. The same as the MCP tools
// fetch_page, mail_messages / read_mail and query_database.

type fetchResult struct{ core.FetchResult }

func (r fetchResult) Text(w io.Writer) {
	for _, rd := range r.Redirects {
		fmt.Fprintf(w, "%d → %s\n", rd.Status, rd.URL)
	}
	if r.External != "" {
		fmt.Fprintf(w, "Redirects outside Benbow (not followed): %s\n", r.External)
	}
	fmt.Fprintf(w, "%d %s (%d bytes, %d ms)\n", r.Status, r.URL, r.Bytes, r.DurationMS)
	if r.Title != "" {
		fmt.Fprintf(w, "Title: %s\n", r.Title)
	}
	for _, m := range r.PHP {
		where := ""
		if m.File != "" {
			where = fmt.Sprintf(" (%s:%d)", m.File, m.Line)
		}
		fmt.Fprintf(w, "PHP %s: %s%s\n", m.Level, m.Message, where)
	}
	if len(r.PHP) == 0 {
		fmt.Fprintln(w, "No PHP errors, warnings or deprecations.")
	}
	if r.HTML != "" {
		fmt.Fprintln(w, r.HTML)
	}
}

func (a *app) fetchCmd() *cobra.Command {
	var head, html bool
	cmd := &cobra.Command{
		Use:   "fetch <site> [path]",
		Short: "Request a page of a site and show the status, redirects and PHP errors it caused",
		Args:  cobra.RangeArgs(1, 2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			req := core.FetchRequest{Site: args[0], HTML: html}
			if len(args) == 2 {
				req.Path = args[1]
			}
			if head {
				req.Method = "HEAD"
			}
			r, err := c.FetchPage(ctx, req)
			return fetchResult{r}, err
		}),
	}
	cmd.Flags().BoolVar(&head, "head", false, "HEAD instead of GET")
	cmd.Flags().BoolVar(&html, "html", false, "print the page's HTML")
	return cmd
}

type mailListResult []core.MailMessage

func (r mailListResult) Text(w io.Writer) {
	if len(r) == 0 {
		fmt.Fprintln(w, "No mail.")
	}
	for _, m := range r {
		fmt.Fprintf(w, "%s  %s  %-30s %s\n", m.ID, m.Date.Local().Format("2006-01-02 15:04"), strings.Join(m.To, ", "), m.Subject)
	}
}

type mailShowResult struct{ core.MailContent }

func (r mailShowResult) Text(w io.Writer) {
	fmt.Fprintf(w, "From: %s\nTo: %s\nSubject: %s\nDate: %s\n\n%s\n", r.From, strings.Join(r.To, ", "), r.Subject,
		r.Date.Local().Format("2006-01-02 15:04"), r.MailContent.Text)
	if len(r.Links) > 0 {
		fmt.Fprintln(w, "\nLinks:")
		for _, l := range r.Links {
			fmt.Fprintln(w, "  "+l)
		}
	}
}

func (a *app) mailReadCmds() []*cobra.Command {
	var q core.MailQuery
	list := &cobra.Command{
		Use:   "list",
		Short: "The newest mail PHP sent (filter by --to or --subject)",
		Args:  cobra.NoArgs,
		RunE: a.withCore(func(ctx context.Context, c *core.Core, _ []string) (Result, error) {
			l, err := c.MailMessages(ctx, q)
			return mailListResult(l), err
		}),
	}
	list.Flags().StringVar(&q.To, "to", "", "only mail to this address")
	list.Flags().StringVar(&q.Subject, "subject", "", "only mail with these words in the subject")
	list.Flags().IntVarP(&q.Limit, "limit", "n", 20, "how many")
	show := &cobra.Command{
		Use:   "show [id]",
		Short: "One message's text and links (the newest without an id)",
		Args:  cobra.MaximumNArgs(1),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			id := ""
			if len(args) == 1 {
				id = args[0]
			}
			m, err := c.MailMessage(ctx, id)
			return mailShowResult{m}, err
		}),
	}
	return []*cobra.Command{list, show}
}

type queryResult core.QueryResult

func (r queryResult) Text(w io.Writer) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(r.Columns, "\t"))
	for _, row := range r.Rows {
		fmt.Fprintln(tw, strings.Join(row, "\t"))
	}
	_ = tw.Flush()
	more := ""
	if r.Truncated {
		more = " (more not shown)"
	}
	fmt.Fprintf(w, "%d row(s)%s, %d ms\n", len(r.Rows), more, r.MS)
}

func (a *app) dbQueryCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "query <site> <sql>",
		Short: "Run one read-only query (SELECT, SHOW, DESCRIBE, EXPLAIN, WITH) on a site's database",
		Long: "Runs as a user that can only read, in a read-only transaction: at most 500 rows and\n" +
			"10 seconds. Changes go through wp or artisan.",
		Args: cobra.ExactArgs(2),
		RunE: a.withCore(func(ctx context.Context, c *core.Core, args []string) (Result, error) {
			r, err := c.QueryDatabase(ctx, args[0], args[1])
			return queryResult(r), err
		}),
	}
}
