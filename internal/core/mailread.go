// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
)

// Reading the mail PHP sent (Mailpit's API on loopback), so an agent can
// check that a form or a notification works.

// MailMessage is a message in the list.
type MailMessage struct {
	ID          string    `json:"id"`
	From        string    `json:"from"`
	To          []string  `json:"to"`
	Subject     string    `json:"subject"`
	Date        time.Time `json:"date"`
	Snippet     string    `json:"snippet,omitempty"`
	Attachments int       `json:"attachments,omitempty"`
}

// MailContent is one message: its text and the links in it.
type MailContent struct {
	MailMessage
	Text        string   `json:"text"`
	Links       []string `json:"links"`
	Attachments []string `json:"attachment_names,omitempty"`
}

// MailQuery filters the list (empty: the newest messages).
type MailQuery struct {
	To      string
	Subject string
	Limit   int
}

type mailpitAddr struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

func (a mailpitAddr) String() string {
	if a.Name == "" {
		return a.Address
	}
	return a.Name + " <" + a.Address + ">"
}

type mailpitSummary struct {
	ID          string        `json:"ID"`
	From        mailpitAddr   `json:"From"`
	To          []mailpitAddr `json:"To"`
	Subject     string        `json:"Subject"`
	Created     time.Time     `json:"Created"`
	Date        time.Time     `json:"Date"`
	Snippet     string        `json:"Snippet"`
	Attachments int           `json:"Attachments"`
}

func (m mailpitSummary) message() MailMessage {
	out := MailMessage{ID: m.ID, From: m.From.String(), Subject: m.Subject, Date: m.Date, Snippet: m.Snippet, Attachments: m.Attachments, To: []string{}}
	if out.Date.IsZero() {
		out.Date = m.Created
	}
	for _, t := range m.To {
		out.To = append(out.To, t.String())
	}
	return out
}

func (c *Core) mailpitGet(ctx context.Context, path string, v any) error {
	on, err := c.mailEnabled(ctx)
	if err != nil {
		return err
	}
	if !on {
		return errcode.Errorf(errcode.NotFound, "Mail is off: the user can turn it on in Benbow (Settings → Mail) or with `benbow mail enable`")
	}
	_, ui, err := c.mailPorts(ctx)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(ui)+path, nil)
	if err != nil {
		return fmt.Errorf("mailpit request: %w", err)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("mail catcher (Mailpit) does not answer, is Benbow started? %w", err))
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errcode.Errorf(errcode.NotFound, "no such message")
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return errcode.Newf(errcode.Service, "Mailpit: %s %s", resp.Status, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 20<<20)).Decode(v); err != nil {
		return fmt.Errorf("read Mailpit's answer: %w", err)
	}
	return nil
}

// MailMessages lists the newest messages Mailpit caught, optionally only
// those to an address or with words in the subject.
func (c *Core) MailMessages(ctx context.Context, q MailQuery) ([]MailMessage, error) {
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 20
	}
	var terms []string
	if q.To != "" {
		terms = append(terms, "to:"+quoteSearch(q.To))
	}
	if q.Subject != "" {
		terms = append(terms, "subject:"+quoteSearch(q.Subject))
	}
	path := "/api/v1/messages?limit=" + strconv.Itoa(limit)
	if len(terms) > 0 {
		path = "/api/v1/search?limit=" + strconv.Itoa(limit) + "&query=" + url.QueryEscape(strings.Join(terms, " "))
	}
	var res struct {
		Messages []mailpitSummary `json:"messages"`
	}
	if err := c.mailpitGet(ctx, path, &res); err != nil {
		return nil, err
	}
	out := make([]MailMessage, 0, len(res.Messages))
	for _, m := range res.Messages {
		out = append(out, m.message())
	}
	return out, nil
}

func quoteSearch(s string) string {
	s = strings.ReplaceAll(s, `"`, "")
	if strings.ContainsAny(s, " \t") {
		return `"` + s + `"`
	}
	return s
}

var reHref = regexp.MustCompile(`(?i)href\s*=\s*["']([^"']+)["']`)
var reURL = regexp.MustCompile(`https?://[^\s<>"')\]]+`)

// MailMessage returns one message's text and links ("latest" for the
// newest).
func (c *Core) MailMessage(ctx context.Context, id string) (MailContent, error) {
	if id == "" {
		id = "latest"
	}
	if id != "latest" && !regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`).MatchString(id) {
		return MailContent{}, errcode.Errorf(errcode.InvalidInput, "bad message id")
	}
	var m struct {
		mailpitSummary
		Text        string `json:"Text"`
		HTML        string `json:"HTML"`
		Attachments []struct {
			FileName string `json:"FileName"`
		} `json:"Attachments"`
	}
	if err := c.mailpitGet(ctx, "/api/v1/message/"+id, &m); err != nil {
		return MailContent{}, err
	}
	out := MailContent{MailMessage: m.message(), Text: m.Text, Links: []string{}}
	if out.Text == "" && m.HTML != "" {
		_, out.Text = pageText(m.HTML)
	}
	if len(out.Text) > maxFetchText*4 {
		out.Text = out.Text[:maxFetchText*4] + "\n… (cut)"
	}
	seen := map[string]bool{}
	add := func(l string) {
		l = strings.TrimRight(l, ".,;")
		if !seen[l] && len(out.Links) < 100 {
			seen[l] = true
			out.Links = append(out.Links, l)
		}
	}
	for _, mm := range reHref.FindAllStringSubmatch(m.HTML, -1) {
		if strings.HasPrefix(mm[1], "http") {
			add(mm[1])
		}
	}
	for _, l := range reURL.FindAllString(m.Text, -1) {
		add(l)
	}
	out.MailMessage.Attachments = len(m.Attachments)
	for _, a := range m.Attachments {
		out.Attachments = append(out.Attachments, a.FileName)
	}
	return out, nil
}
