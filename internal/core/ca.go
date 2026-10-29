// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/getcovo/covo/internal/ca"
	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/paths"
	"github.com/getcovo/covo/internal/trust"
)

func caDir(l paths.Layout) ca.Dir { return ca.Dir(filepath.Join(l.Data, "ca")) }

// legacyPKIDir is where Caddy kept the unconstrained CA it generated during
// the spike. It is only ever read to untrust and delete that CA.
func legacyPKIDir(l paths.Layout) string { return filepath.Join(l.Data, "caddy", "pki") }

func legacyCACertPath(l paths.Layout) string {
	return filepath.Join(legacyPKIDir(l), "authorities", "local", "root.crt")
}

// caCertPaths lists every root certificate file Covo may have created.
func caCertPaths(l paths.Layout) []string {
	return []string{caDir(l).RootCert(), legacyCACertPath(l)}
}

// CACertPath is the root certificate of Covo's CA.
func (c *Core) CACertPath() string { return caDir(c.Layout).RootCert() }

func (c *Core) caBundlePath() string { return filepath.Join(c.Layout.Etc, "ca-bundle.pem") }

// ensureCA makes sure Covo's constrained CA exists. created means a new root,
// which is not trusted yet.
func (c *Core) ensureCA() (created bool, err error) {
	return ca.Ensure(caDir(c.Layout), ca.Name(), time.Now())
}

func (c *Core) caddyCA() caddy.CA {
	d := caDir(c.Layout)
	return caddy.CA{
		Name:             ca.Name(),
		RootCert:         d.RootCert(),
		IntermediateCert: d.IntermediateCert(),
		IntermediateKey:  d.IntermediateKey(),
	}
}

// caFingerprint is the SHA-256 of the root file, written into the Caddyfile
// so that a new CA changes it and makes a running Caddy reload.
func (c *Core) caFingerprint() (string, error) {
	b, err := os.ReadFile(c.CACertPath())
	if err != nil {
		return "", fmt.Errorf("read CA: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// ensureCABundle writes the CA bundle PHP verifies TLS with: the pinned
// Mozilla roots plus Covo's root. It returns a short hash of its content.
func (c *Core) ensureCABundle(ctx context.Context) (string, error) {
	art, err := c.Manifest.Resolve("cabundle", "")
	if err != nil {
		return "", err
	}
	inst, err := c.Installer.Install(ctx, art)
	if err != nil {
		return "", err
	}
	mozilla, err := os.ReadFile(filepath.Join(inst.Dir, art.Files[0]))
	if err != nil {
		return "", fmt.Errorf("read CA bundle: %w", err)
	}
	root, err := os.ReadFile(c.CACertPath())
	if err != nil {
		return "", fmt.Errorf("read CA: %w", err)
	}
	body := ca.Bundle(mozilla, root, ca.Name())
	if _, err := fsutil.WriteAtomic(c.caBundlePath(), body, 0o644); err != nil {
		return "", err
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:8]), nil
}

// readCAs returns the root certificates found on disk among files.
func readCAs(files []string) []trust.Cert {
	var out []trust.Cert
	for _, f := range files {
		if c, err := trust.ReadCert(f); err == nil {
			out = append(out, c)
		}
	}
	return out
}

// trustedCAs lists the certificates to remove from the keychain: those on
// disk that are trusted or, if no file is left, every certificate named name.
func trustedCAs(ctx context.Context, kc *trust.Keychain, name string, files []string) ([]trust.Cert, error) {
	onDisk := readCAs(files)
	if len(onDisk) == 0 {
		return kc.Find(ctx, name)
	}
	var out []trust.Cert
	for _, c := range onDisk {
		ok, err := kc.Trusted(ctx, c)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, c)
		}
	}
	return out, nil
}

// removeCAs removes Covo's CAs from the keychain: the ones on disk by SHA-1
// or, if no file is left, every certificate named name.
func removeCAs(ctx context.Context, kc *trust.Keychain, name string, files []string) ([]trust.Cert, error) {
	onDisk := readCAs(files)
	if len(onDisk) == 0 {
		return kc.Remove(ctx, name, nil)
	}
	var removed []trust.Cert
	for _, c := range onDisk {
		r, err := kc.Remove(ctx, name, &c)
		removed = append(removed, r...)
		if err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// removeLegacyPKI deletes the spike-era CA files. Callers untrust it first.
func (c *Core) removeLegacyPKI() error {
	if err := os.RemoveAll(legacyPKIDir(c.Layout)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove old CA: %w", err)
	}
	return nil
}
