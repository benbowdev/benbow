// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/trust"
)

const (
	settingCASHA1   = "ca_sha1"
	settingCASHA256 = "ca_sha256"
)

// TrustResult reports the CA after trust or untrust.
type TrustResult struct {
	CertPath string       `json:"cert_path"`
	Cert     *trust.Cert  `json:"cert,omitempty"`
	Trusted  bool         `json:"trusted"`
	Changed  bool         `json:"changed"`
	Removed  []trust.Cert `json:"removed,omitempty"`
}

// Trust adds Covo's local CA to the login keychain as a trusted root.
// beforePrompt is called right before macOS shows its authorization dialog,
// and not at all if the CA is already trusted.
func (c *Core) Trust(ctx context.Context, beforePrompt func()) (TrustResult, error) {
	var res TrustResult
	err := c.locked(ctx, func() error {
		if err := c.ensureCA(ctx); err != nil {
			return err
		}
		cert, err := trust.ReadCert(c.CACertPath())
		if err != nil {
			return err
		}
		res = TrustResult{CertPath: c.CACertPath(), Cert: &cert}
		kc, err := trust.Login()
		if err != nil {
			return err
		}
		ok, err := kc.Trusted(ctx, cert)
		if err != nil {
			return err
		}
		if !ok {
			if beforePrompt != nil {
				beforePrompt()
			}
			if err := kc.Add(ctx, cert); err != nil {
				return err
			}
			if ok, err = kc.Trusted(ctx, cert); err != nil {
				return err
			}
			if !ok {
				return errcode.Errorf(errcode.Service, "the CA was added but macOS does not report it as trusted")
			}
			res.Changed = true
		}
		res.Trusted = true
		if err := c.Store.SetSetting(ctx, settingCASHA1, cert.SHA1); err != nil {
			return err
		}
		return c.Store.SetSetting(ctx, settingCASHA256, cert.SHA256)
	})
	return res, err
}

// Untrust removes Covo's CA from the login keychain: the certificate on disk
// by SHA-1 or, if the file is gone, every certificate named caddy.CAName().
// It is a no-op when nothing is there.
func (c *Core) Untrust(ctx context.Context) (TrustResult, error) {
	var res TrustResult
	err := c.locked(ctx, func() error {
		var err error
		res, err = c.untrust(ctx)
		return err
	})
	return res, err
}

func (c *Core) untrust(ctx context.Context) (TrustResult, error) {
	res := TrustResult{CertPath: c.CACertPath()}
	kc, err := trust.Login()
	if err != nil {
		return res, err
	}
	var onDisk *trust.Cert
	if cert, err := trust.ReadCert(c.CACertPath()); err == nil {
		onDisk = &cert
	}
	removed, err := kc.Remove(ctx, caddy.CAName(), onDisk)
	if err != nil {
		return res, err
	}
	res.Removed = removed
	res.Changed = len(removed) > 0
	_ = c.Store.DeleteSetting(ctx, settingCASHA1)
	_ = c.Store.DeleteSetting(ctx, settingCASHA256)
	return res, nil
}

// caTrusted reports whether the CA on disk is trusted (false if absent).
func (c *Core) caTrusted(ctx context.Context) (bool, error) {
	cert, err := trust.ReadCert(c.CACertPath())
	if errcode.Code(err) == errcode.NotFound {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	kc, err := trust.Login()
	if err != nil {
		return false, err
	}
	return kc.Trusted(ctx, cert)
}

// ensureCA makes Caddy create its local CA if it does not exist yet, by
// validating the current configuration (validation provisions the PKI app).
func (c *Core) ensureCA(ctx context.Context) error {
	if _, err := os.Stat(c.CACertPath()); err == nil {
		return c.securePKI()
	}
	inst, err := c.ensureCaddy(ctx)
	if err != nil {
		return err
	}
	port, _, err := c.httpsPort(ctx)
	if err != nil {
		return err
	}
	sites, err := c.Store.ListSites(ctx)
	if err != nil {
		return err
	}
	body, err := c.renderCaddyfile(port, sites)
	if err != nil {
		return err
	}
	tmp := filepath.Join(c.Layout.Tmp, "Caddyfile.ca")
	if _, err := fsutil.WriteAtomic(tmp, []byte(body), 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := c.caddyBinary(inst).Validate(ctx, tmp); err != nil {
		return err
	}
	if _, err := os.Stat(c.CACertPath()); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("caddy did not create its CA: %w", err))
	}
	return c.securePKI()
}

// securePKI restricts Caddy's PKI directory: 0700 directories, 0600 files.
func (c *Core) securePKI() error {
	root := filepath.Join(c.Layout.Data, "caddy", "pki")
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		mode := os.FileMode(0o600)
		if d.IsDir() {
			mode = 0o700
		}
		return os.Chmod(path, mode)
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("secure %s: %w", root, err)
	}
	return nil
}
