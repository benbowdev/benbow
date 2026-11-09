// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/store"
)

// fetch_page: Covo requests a page of one of its sites, the way a browser
// would, and returns the status, headers, redirects, the page's text and
// the PHP errors, warnings and deprecations that request produced. Each
// request carries an X-Covo-Probe header: Caddy then gives that request
// its own PHP error log (caddy.Site.ProbeDir), so errors are matched to it
// exactly. Sites cost nothing extra when Covo does not fetch.

const (
	maxFetchRedirects = 10
	maxFetchBody      = 4 << 20
	maxFetchText      = 8 << 10
	fetchTimeout      = 60 * time.Second
)

func (c *Core) probeDir() string { return filepath.Join(c.Layout.Tmp, "probes") }

// FetchRequest is one page to fetch.
type FetchRequest struct {
	Site string
	// Path is a path on the site ("/shop/?p=1") or a full URL on any Covo
	// site's address.
	Path   string
	Method string // GET (default) or HEAD
	// HTML returns the page's HTML too (cut at 100 KiB).
	HTML bool
}

// FetchResult is what the page returned.
type FetchResult struct {
	URL        string            `json:"url"`
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Redirects  []FetchRedirect   `json:"redirects,omitempty"`
	External   string            `json:"external_redirect,omitempty"`
	Title      string            `json:"title,omitempty"`
	Text       string            `json:"text,omitempty"`
	HTML       string            `json:"html,omitempty"`
	Bytes      int               `json:"bytes"`
	DurationMS int64             `json:"duration_ms"`
	// PHP lists what PHP logged for these requests (redirects included).
	PHP []PHPMessage `json:"php"`
	// AppOutput is what an app's dev server printed while answering (apps
	// Covo starts), in place of PHP errors.
	AppOutput []string `json:"app_output,omitempty"`
}

// FetchRedirect is one redirect followed.
type FetchRedirect struct {
	URL    string `json:"url"`
	Status int    `json:"status"`
}

// PHPMessage is one line of PHP's error log.
type PHPMessage struct {
	Level   string `json:"level"` // fatal, error, warning, notice, deprecated, other
	Message string `json:"message"`
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// FetchPage requests a page of a Covo site (never another address).
func (c *Core) FetchPage(ctx context.Context, req FetchRequest) (FetchResult, error) {
	var res FetchResult
	site, err := c.Store.Site(ctx, req.Site)
	if err != nil {
		return res, err
	}
	method := strings.ToUpper(req.Method)
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodHead {
		return res, errcode.Errorf(errcode.InvalidInput, "only GET and HEAD: changes go through wp, artisan or the site itself")
	}
	ports, _, err := c.ports(ctx)
	if err != nil {
		return res, err
	}
	hosts, err := c.covoHosts(ctx)
	if err != nil {
		return res, err
	}
	target, err := fetchTarget(siteURL(site, ports.HTTPS), req.Path, hosts)
	if err != nil {
		return res, err
	}
	client, err := c.fetchClient(ports.HTTPSListen)
	if err != nil {
		return res, err
	}
	if err := os.MkdirAll(c.probeDir(), 0o700); err != nil {
		return res, fmt.Errorf("create %s: %w", c.probeDir(), err)
	}
	idb := make([]byte, 16)
	_, _ = rand.Read(idb)
	id := hex.EncodeToString(idb)
	probe := filepath.Join(c.probeDir(), id+".log")
	defer os.Remove(probe)

	appLog, appFrom := "", int64(-1)
	if site.IsApp() && site.AppMode == store.AppRun {
		appLog = c.siteLogPath(site.Name, LogApp)
		appFrom = 0
		if fi, err := os.Stat(appLog); err == nil {
			appFrom = fi.Size()
		}
	}

	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	start := time.Now()
	var resp *http.Response
	for hop := 0; ; hop++ {
		r, err := http.NewRequestWithContext(ctx, method, target.String(), nil)
		if err != nil {
			return res, errcode.New(errcode.InvalidInput, err)
		}
		r.Header.Set(caddy.ProbeHeader, id)
		r.Header.Set("User-Agent", "Covo fetch_page")
		resp, err = client.Do(r)
		if err != nil {
			return res, errcode.New(errcode.Service, fmt.Errorf("request %s: %w (is Covo started and the site on?)", target, err))
		}
		loc := resp.Header.Get("Location")
		if resp.StatusCode < 300 || resp.StatusCode >= 400 || loc == "" {
			break
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		next, err := target.Parse(loc)
		if err != nil {
			break
		}
		res.Redirects = append(res.Redirects, FetchRedirect{URL: next.String(), Status: resp.StatusCode})
		if next.Scheme != "https" || !slices.Contains(hosts, strings.ToLower(next.Hostname())) {
			res.External = next.String()
			break
		}
		if hop+1 >= maxFetchRedirects {
			return res, errcode.Newf(errcode.Service, "more than %d redirects", maxFetchRedirects)
		}
		target = next
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxFetchBody))
	res.DurationMS = time.Since(start).Milliseconds()
	res.URL, res.Status, res.Bytes = target.String(), resp.StatusCode, len(body)
	res.Headers = map[string]string{}
	for _, h := range []string{"Content-Type", "Content-Length", "Location", "Cache-Control", "X-Powered-By", "X-Redirect-By", "Link", "Retry-After"} {
		if v := resp.Header.Get(h); v != "" {
			res.Headers[h] = v
		}
	}
	if cookies := resp.Cookies(); len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for _, ck := range cookies {
			names = append(names, ck.Name)
		}
		res.Headers["Set-Cookie (names)"] = strings.Join(names, ", ")
	}
	if strings.Contains(resp.Header.Get("Content-Type"), "html") || strings.Contains(resp.Header.Get("Content-Type"), "text") {
		res.Title, res.Text = pageText(string(body))
		if req.HTML {
			res.HTML = string(body)
			if len(res.HTML) > maxOutputHTML {
				res.HTML = res.HTML[:maxOutputHTML] + "\n… (cut at 100 KiB)"
			}
		}
	}
	res.PHP = []PHPMessage{}
	if b, err := os.ReadFile(probe); err == nil && len(b) > 0 {
		res.PHP = parsePHPLog(string(b))
		// The site's own log keeps everything, as for any request.
		if f, err := os.OpenFile(c.siteLogPath(site.Name, LogPHP), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write(b)
			_ = f.Close()
		}
	}
	if appLog != "" {
		res.AppOutput = appOutputSince(appLog, appFrom)
	}
	c.TouchAgentSession(ctx, site.Name)
	return res, nil
}

// appOutputSince returns the lines an app wrote to its log after offset
// from, waiting a moment for output written just after the answer.
func appOutputSince(log string, from int64) []string {
	time.Sleep(300 * time.Millisecond)
	out := []string{}
	f, err := os.Open(log)
	if err != nil {
		return out
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.Size() < from {
		return out // emptied or rotated meanwhile
	}
	b, _ := io.ReadAll(io.LimitReader(io.NewSectionReader(f, from, fi.Size()-from), 64<<10))
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		if l != "" && !strings.HasPrefix(l, appStartMarker) {
			out = append(out, l)
		}
	}
	return out[max(len(out)-100, 0):]
}

const maxOutputHTML = 100 << 10

// covoHosts are the domains and aliases of every site.
func (c *Core) covoHosts(ctx context.Context) ([]string, error) {
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var hosts []string
	for _, s := range sites {
		hosts = append(hosts, s.Domain)
		hosts = append(hosts, s.Aliases...)
	}
	return hosts, nil
}

// fetchTarget resolves path against the site's address; a full URL must
// be on a Covo site.
func fetchTarget(base, path string, hosts []string) (*url.URL, error) {
	b, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", base, err)
	}
	if path == "" {
		path = "/"
	}
	u, err := b.Parse(path)
	if err != nil {
		return nil, errcode.Newf(errcode.InvalidInput, "bad path %q", path)
	}
	if u.Scheme == "http" && slices.Contains(hosts, strings.ToLower(u.Hostname())) {
		u.Scheme, u.Host = "https", strings.ToLower(u.Hostname())+portSuffix(b)
	}
	if u.Scheme != "https" || !slices.Contains(hosts, strings.ToLower(u.Hostname())) {
		return nil, errcode.Newf(errcode.Forbidden, "%s is not a Covo site: fetch_page only opens Covo's own sites", u.Redacted())
	}
	return u, nil
}

func portSuffix(u *url.URL) string {
	if p := u.Port(); p != "" {
		return ":" + p
	}
	return ""
}

// fetchClient connects to Caddy on loopback whatever the address resolves
// to, trusting only Covo's CA, and follows no redirect by itself.
func (c *Core) fetchClient(listen int) (*http.Client, error) {
	pem, err := os.ReadFile(c.caddyCA().RootCert)
	if err != nil {
		return nil, errcode.New(errcode.Service, fmt.Errorf("read Covo's CA: %w", err))
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, errcode.Errorf(errcode.Service, "Covo's CA certificate is not valid")
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return dialer.DialContext(ctx, network, "127.0.0.1:"+strconv.Itoa(listen))
		},
		Proxy:                 nil,
		ResponseHeaderTimeout: fetchTimeout,
	}
	return &http.Client{
		Transport:     tr,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}, nil
}

var (
	reTitle   = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	reDrop    = regexp.MustCompile(`(?is)<(script|style|noscript|template|svg)[^>]*>.*?</(script|style|noscript|template|svg)>`)
	reComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	reBlock   = regexp.MustCompile(`(?i)</?(p|div|br|li|ul|ol|h[1-6]|tr|table|section|article|header|footer|nav|main|form|label|button)[^>]*>`)
	reTag     = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpaces  = regexp.MustCompile(`[ \t\r\f\v]+`)
	reLines   = regexp.MustCompile(`\n\s*\n+`)
)

// pageText returns a page's title and readable text (scripts and styles
// left out), cut at 8 KiB.
func pageText(page string) (string, string) {
	title := ""
	if m := reTitle.FindStringSubmatch(page); m != nil {
		title = strings.TrimSpace(html.UnescapeString(reTag.ReplaceAllString(m[1], "")))
	}
	t := reDrop.ReplaceAllString(page, " ")
	t = reComment.ReplaceAllString(t, " ")
	t = reBlock.ReplaceAllString(t, "\n")
	t = reTag.ReplaceAllString(t, " ")
	t = html.UnescapeString(t)
	t = reSpaces.ReplaceAllString(t, " ")
	lines := strings.Split(t, "\n")
	for i := range lines {
		lines[i] = strings.TrimSpace(lines[i])
	}
	t = strings.TrimSpace(reLines.ReplaceAllString(strings.Join(lines, "\n"), "\n"))
	if len(t) > maxFetchText {
		t = t[:maxFetchText] + "\n… (text cut at 8 KiB)"
	}
	return title, t
}

// "[04-Nov-2026 14:02:03 UTC] PHP Warning:  Undefined variable $x in /path/file.php on line 3"
var rePHPLog = regexp.MustCompile(`^\[[^\]]+\] PHP ([A-Za-z ]+?):\s+(.*?)(?: in (\S.*?)(?: on line |:)(\d+))?$`)

// parsePHPLog splits a PHP error log into messages (stack trace lines are
// kept with their message).
func parsePHPLog(log string) []PHPMessage {
	out := []PHPMessage{}
	for _, l := range strings.Split(strings.TrimRight(log, "\n"), "\n") {
		m := rePHPLog.FindStringSubmatch(l)
		if m == nil {
			if len(out) > 0 && l != "" {
				out[len(out)-1].Message += "\n" + l
			}
			continue
		}
		msg := PHPMessage{Level: phpLevel(m[1]), Message: m[2], File: m[3]}
		msg.Line, _ = strconv.Atoi(m[4])
		out = append(out, msg)
		if len(out) >= 200 {
			break
		}
	}
	return out
}

func phpLevel(s string) string {
	s = strings.ToLower(s)
	switch {
	case strings.Contains(s, "fatal"), strings.Contains(s, "parse"):
		return "fatal"
	case strings.Contains(s, "deprecated"):
		return "deprecated"
	case strings.Contains(s, "warning"):
		return "warning"
	case strings.Contains(s, "notice"), strings.Contains(s, "strict"):
		return "notice"
	case strings.Contains(s, "error"):
		return "error"
	}
	return "other"
}
