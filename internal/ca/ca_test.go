// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var now = time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

func mustGenerate(t *testing.T) Material {
	t.Helper()
	m, err := Generate("Covo Test CA", now)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// leaf signs a server certificate for name with the intermediate, as Caddy does.
func leaf(t *testing.T, m Material, name string) *x509.Certificate {
	t.Helper()
	inter, err := parseCert(m.IntermediateCert)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(m.IntermediateKey)
	key, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	lk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(42),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(12 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	if ip := net.ParseIP(name); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{name}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, inter, &lk.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNameConstraints(t *testing.T) {
	m := mustGenerate(t)
	root, _ := parseCert(m.RootCert)
	inter, _ := parseCert(m.IntermediateCert)
	roots, inters := x509.NewCertPool(), x509.NewCertPool()
	roots.AddCert(root)
	inters.AddCert(inter)

	tests := []struct {
		name string
		ok   bool
	}{
		{"demo.localhost", true},
		{"demo.covo.localhost", true},
		{"demo.test", true},
		{"demo.covo.test", true},
		{"studio.local", true},
		{"example.com", false},
		{"localhost.example.com", false},
		{"test.example.com", false},
		{"demo.covo", false},
		{"google.com", false},
		{"127.0.0.1", false},
		{"::1", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := leaf(t, m, tt.name).Verify(x509.VerifyOptions{
				Roots: roots, Intermediates: inters, CurrentTime: now,
			})
			if tt.ok && err != nil {
				t.Fatalf("rejected: %v", err)
			}
			if !tt.ok && err == nil {
				t.Fatal("accepted, want rejected by name constraints")
			}
		})
	}
}

func TestGenerateShape(t *testing.T) {
	m := mustGenerate(t)
	if err := Check(m, "Covo Test CA", now); err != nil {
		t.Fatal(err)
	}
	root, _ := parseCert(m.RootCert)
	inter, _ := parseCert(m.IntermediateCert)
	if !Constrained(root) || !Constrained(inter) {
		t.Fatal("root and intermediate must both be constrained")
	}
	if !inter.MaxPathLenZero || inter.MaxPathLen != 0 {
		t.Error("intermediate must not sign further CAs")
	}
	if !inter.NotAfter.Before(root.NotAfter) {
		t.Error("intermediate must expire before the root")
	}
}

func TestCheckRejects(t *testing.T) {
	m := mustGenerate(t)
	other := mustGenerate(t)
	tests := []struct {
		name string
		m    Material
		cn   string
		at   time.Time
	}{
		{"wrong name", m, "Someone Else", now},
		{"near expiry", m, "Covo Test CA", now.Add(validity - 10*24*time.Hour)},
		{"foreign intermediate", Material{m.RootCert, other.IntermediateCert, other.IntermediateKey}, "Covo Test CA", now},
		{"mismatched key", Material{m.RootCert, m.IntermediateCert, other.IntermediateKey}, "Covo Test CA", now},
		{"unconstrained root", Material{unconstrainedRoot(t), m.IntermediateCert, m.IntermediateKey}, "Covo Test CA", now},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Check(tt.m, tt.cn, tt.at); err == nil {
				t.Fatal("Check accepted an invalid CA")
			}
		})
	}
}

// unconstrainedRoot is what Caddy generated during the spike.
func unconstrainedRoot(t *testing.T) []byte {
	t.Helper()
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Covo Test CA"},
		NotBefore: now.Add(-time.Hour), NotAfter: now.Add(validity),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestEnsure(t *testing.T) {
	d := Dir(filepath.Join(t.TempDir(), "ca"))
	created, err := Ensure(d, "Covo Test CA", now)
	if err != nil || !created {
		t.Fatalf("first Ensure: created=%v err=%v", created, err)
	}
	first, err := Load(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{string(d), d.RootCert(), d.IntermediateCert(), d.IntermediateKey()} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("%s has mode %v, want no group/other access", p, perm)
		}
	}

	created, err = Ensure(d, "Covo Test CA", now.Add(24*time.Hour))
	if err != nil || created {
		t.Fatalf("second Ensure must keep the CA: created=%v err=%v", created, err)
	}

	// A spike-era (unconstrained) root is replaced.
	if err := os.WriteFile(d.RootCert(), unconstrainedRoot(t), 0o600); err != nil {
		t.Fatal(err)
	}
	created, err = Ensure(d, "Covo Test CA", now)
	if err != nil || !created {
		t.Fatalf("Ensure must replace an unconstrained CA: created=%v err=%v", created, err)
	}
	second, _ := Load(d)
	if string(second.RootCert) == string(first.RootCert) {
		t.Fatal("root was not regenerated")
	}
}

func TestBundle(t *testing.T) {
	m := mustGenerate(t)
	b := string(Bundle([]byte("MOZILLA\n\n"), m.RootCert, "Covo Test CA"))
	if !strings.HasPrefix(b, "MOZILLA\n\nCovo Test CA (added by Covo)\n-----BEGIN CERTIFICATE-----") {
		t.Fatalf("unexpected bundle:\n%s", b)
	}
}
