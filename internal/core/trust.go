// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"

	"github.com/getcovo/covo/internal/ca"
	"github.com/getcovo/covo/internal/errcode"
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

// Trust adds Covo's local CA to the login keychain as a trusted root, and
// removes the unconstrained CA left by the spike, if any. beforePrompt is
// called once, right before macOS may show its authorization dialog, and not
// at all if there is nothing to change.
func (c *Core) Trust(ctx context.Context, beforePrompt func()) (TrustResult, error) {
	var res TrustResult
	prompted := false
	prompt := func() {
		if !prompted && beforePrompt != nil {
			beforePrompt()
		}
		prompted = true
	}
	err := c.locked(ctx, func() error {
		created, err := c.ensureCA()
		if err != nil {
			return err
		}
		if created {
			// A running Caddy must switch to the new CA.
			if _, running, err := c.Store.Setting(ctx, settingRunning); err != nil {
				return err
			} else if running {
				if _, err := c.reconcile(ctx); err != nil {
					return err
				}
			}
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

		if legacy, err := trust.ReadCert(legacyCACertPath(c.Layout)); err == nil {
			ok, err := kc.Trusted(ctx, legacy)
			if err != nil {
				return err
			}
			if ok {
				prompt()
				removed, err := kc.Remove(ctx, ca.Name(), &legacy)
				res.Removed = removed
				if err != nil {
					return err
				}
				res.Changed = true
			}
			if err := c.removeLegacyPKI(); err != nil {
				return err
			}
		}

		ok, err := kc.Trusted(ctx, cert)
		if err != nil {
			return err
		}
		if !ok {
			prompt()
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

// Untrust removes Covo's CAs from the login keychain: the certificates on
// disk (current and spike-era) by SHA-1 or, if no file is left, every
// certificate named ca.Name(). It is a no-op when nothing is there.
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
	removed, err := removeCAs(ctx, kc, ca.Name(), caCertPaths(c.Layout))
	res.Removed = removed
	if err != nil {
		return res, err
	}
	res.Changed = len(removed) > 0
	if err := c.removeLegacyPKI(); err != nil {
		return res, err
	}
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
