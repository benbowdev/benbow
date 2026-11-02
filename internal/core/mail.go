// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/ports"
)

const (
	mailService      = "mailpit"
	settingMail      = "mail_enabled"
	settingMailSMTP  = "mail_smtp_port"
	settingMailUI    = "mail_ui_port"
	defaultMailSMTP  = 1025
	defaultMailUI    = 8025
	mailMaxMessages  = "5000"
	mailExitTimeout  = 10
	mailDataDirName  = "mailpit"
	mailDatabaseName = "mailpit.db"
)

// MailStatus describes Mailpit.
type MailStatus struct {
	Enabled bool   `json:"enabled"`
	Version string `json:"version,omitempty"`
	Running bool   `json:"running"`
	// SMTPPort and UIPort are on 127.0.0.1 only.
	SMTPPort int `json:"smtp_port,omitempty"`
	UIPort   int `json:"ui_port,omitempty"`
	// URL is the inbox, https://mail.localhost:<https port>/.
	URL string `json:"url,omitempty"`
}

// MailResult reports what enable or disable did.
type MailResult struct {
	MailStatus
	Changed bool `json:"changed"`
	Applied bool `json:"applied"`
}

func (c *Core) mailEnabled(ctx context.Context) (bool, error) {
	_, ok, err := c.Store.Setting(ctx, settingMail)
	return ok, err
}

// mailPort returns the stored port for key, choosing a free one from def
// (and different from avoid) the first time.
func (c *Core) mailPort(ctx context.Context, key string, def, avoid int) (int, error) {
	v, ok, err := c.Store.Setting(ctx, key)
	if err != nil {
		return 0, err
	}
	if ok {
		port, err := strconv.Atoi(v)
		if err != nil {
			return 0, fmt.Errorf("bad stored %s %q: %w", key, v, err)
		}
		return port, nil
	}
	port, err := ports.FirstFree(def, portSearchSpan)
	if err != nil {
		return 0, err
	}
	if port == avoid {
		if port, err = ports.FirstFree(port+1, portSearchSpan); err != nil {
			return 0, err
		}
	}
	return port, c.Store.SetSetting(ctx, key, strconv.Itoa(port))
}

func (c *Core) mailPorts(ctx context.Context) (smtp, ui int, err error) {
	if smtp, err = c.mailPort(ctx, settingMailSMTP, defaultMailSMTP, 0); err != nil {
		return 0, 0, err
	}
	ui, err = c.mailPort(ctx, settingMailUI, defaultMailUI, smtp)
	return smtp, ui, err
}

// ensureMailpit installs the pinned Mailpit and returns its binary.
func (c *Core) ensureMailpit(ctx context.Context) (bin, version string, err error) {
	art, err := c.pinned(ctx, "mailpit", "")
	if err != nil {
		return "", "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", "", err
	}
	return filepath.Join(inst.Dir, "mailpit"), art.Version, nil
}

// mailSendmail is what php.ini needs while mail is on: the binary and the
// SMTP port, or "" and 0.
func (c *Core) mailSendmail(ctx context.Context) (string, int, error) {
	on, err := c.mailEnabled(ctx)
	if err != nil || !on {
		return "", 0, err
	}
	bin, _, err := c.ensureMailpit(ctx)
	if err != nil {
		return "", 0, err
	}
	smtp, _, err := c.mailPorts(ctx)
	return bin, smtp, err
}

// mailAgents returns Mailpit's agent while mail is on, with its ports.
func (c *Core) mailAgents(ctx context.Context, loaded map[string]launchd.Status) ([]launchd.Agent, int, int, error) {
	on, err := c.mailEnabled(ctx)
	if err != nil || !on {
		return nil, 0, 0, err
	}
	bin, _, err := c.ensureMailpit(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	smtp, ui, err := c.mailPorts(ctx)
	if err != nil {
		return nil, 0, 0, err
	}
	label := c.labelFor(mailService)
	if !loaded[label].Running {
		for _, p := range []int{smtp, ui} {
			if !ports.Free(p) {
				return nil, 0, 0, errcode.Errorf(errcode.PortInUse, fmt.Sprintf(
					"Mailpit port %d is in use by %s; stop it or run `covo mail disable`", p, ports.Describe(ports.Owners(ctx, p))))
			}
		}
	}
	data := filepath.Join(c.Layout.Data, mailDataDirName)
	if err := os.MkdirAll(data, 0o700); err != nil {
		return nil, 0, 0, fmt.Errorf("create %s: %w", data, err)
	}
	out, errLog := c.logPaths(mailService)
	return []launchd.Agent{{
		Label:   label,
		Program: bin,
		Args: []string{
			"--listen", "127.0.0.1:" + strconv.Itoa(ui),
			"--smtp", "127.0.0.1:" + strconv.Itoa(smtp),
			"--database", filepath.Join(data, mailDatabaseName),
			"--max", mailMaxMessages,
			"--disable-version-check",
		},
		WorkDir:     c.Layout.Root,
		StdoutLog:   out,
		StderrLog:   errLog,
		ExitTimeout: mailExitTimeout,
	}}, smtp, ui, nil
}

func (c *Core) mailStatus(ctx context.Context, httpsPort int) (MailStatus, error) {
	var st MailStatus
	on, err := c.mailEnabled(ctx)
	if err != nil || !on {
		return st, err
	}
	st.Enabled = true
	st.Version = c.pinnedVersion(ctx, "mailpit", "")
	if st.SMTPPort, st.UIPort, err = c.mailPorts(ctx); err != nil {
		return st, err
	}
	loaded, err := c.launchctl.List(ctx)
	if err != nil {
		return st, err
	}
	st.Running = loaded[c.labelFor(mailService)].Running
	st.URL = "https://" + caddy.MailDomain + ":" + strconv.Itoa(cmp.Or(httpsPort, defaultHTTPSPort)) + "/"
	return st, nil
}

// Mail reports Mailpit's state.
func (c *Core) Mail(ctx context.Context) (MailStatus, error) {
	port, _, err := c.httpsPort(ctx)
	if err != nil {
		return MailStatus{}, err
	}
	return c.mailStatus(ctx, port)
}

// SetMail turns Mailpit on or off. While on, PHP's mail() goes to Mailpit
// and the inbox is at https://mail.localhost.
func (c *Core) SetMail(ctx context.Context, on bool) (MailResult, error) {
	var res MailResult
	err := c.locked(ctx, func() error {
		was, err := c.mailEnabled(ctx)
		if err != nil {
			return err
		}
		if on {
			// Download and verify now, so that problems show here.
			if _, _, err := c.ensureMailpit(ctx); err != nil {
				return err
			}
			if _, _, err := c.mailPorts(ctx); err != nil {
				return err
			}
			err = c.Store.SetSetting(ctx, settingMail, "1")
		} else {
			err = c.Store.DeleteSetting(ctx, settingMail)
		}
		if err != nil {
			return err
		}
		res.Changed = was != on
		if res.Applied, err = c.applyIfRunning(ctx); err != nil {
			return err
		}
		port, _, err := c.httpsPort(ctx)
		if err != nil {
			return err
		}
		res.MailStatus, err = c.mailStatus(ctx, port)
		return err
	})
	return res, err
}
