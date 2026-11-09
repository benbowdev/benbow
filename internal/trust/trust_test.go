// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package trust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"strings"
	"testing"
	"time"
)

func makeCert(t *testing.T, cn string) Cert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParsePEM(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// fakeSecurity simulates a keychain plus user trust settings.
type fakeSecurity struct {
	items   map[string]Cert   // by SHA-1
	trusted map[string]string // SHA-1 -> CN
	calls   []string
}

func newFake() *fakeSecurity {
	return &fakeSecurity{items: map[string]Cert{}, trusted: map[string]string{}}
}

func (f *fakeSecurity) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, args[0])
	fail := func(msg string) (string, error) { return msg, errors.New("exit status 44") }
	readArg := func() Cert {
		b, _ := os.ReadFile(args[len(args)-1])
		c, _ := ParsePEM(b)
		return c
	}
	switch args[0] {
	case "find-certificate":
		cn := args[4]
		var b strings.Builder
		for _, c := range f.items {
			if strings.Contains(c.CommonName, cn) {
				b.Write(c.PEM)
			}
		}
		if b.Len() == 0 {
			return fail("security: SecKeychainSearchCopyNext: The specified item could not be found in the keychain.")
		}
		return b.String(), nil
	case "dump-trust-settings":
		if len(f.trusted) == 0 {
			return fail("SecTrustSettingsCopyCertificates: No Trust Settings were found.")
		}
		var b strings.Builder
		b.WriteString("Number of trusted certs = 1\n")
		i := 0
		for _, cn := range f.trusted {
			b.WriteString("Cert " + string(rune('0'+i)) + ": " + cn + "\n")
			i++
		}
		return b.String(), nil
	case "add-trusted-cert":
		c := readArg()
		f.items[c.SHA1] = c
		f.trusted[c.SHA1] = c.CommonName
	case "remove-trusted-cert":
		c := readArg()
		if _, ok := f.trusted[c.SHA1]; !ok {
			return fail("SecTrustSettingsRemoveTrustSettings: The specified item could not be found in the keychain.")
		}
		delete(f.trusted, c.SHA1)
	case "delete-certificate":
		sha := args[2]
		if _, ok := f.items[sha]; !ok {
			return fail("Unable to delete certificate matching \"" + sha + "\"")
		}
		delete(f.items, sha)
	}
	return "", nil
}

func TestTrustLifecycle(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	k := &Keychain{Path: "/tmp/login.keychain-db", Run: f.run}
	root := makeCert(t, "Benbow Local CA")
	other := makeCert(t, "Benbow Local CA Intermediate") // substring match must be ignored
	f.items[other.SHA1] = other

	if ok, err := k.Trusted(ctx, root); err != nil || ok {
		t.Fatalf("before Add: trusted=%v err=%v", ok, err)
	}
	if err := k.Add(ctx, root); err != nil {
		t.Fatal(err)
	}
	if ok, err := k.Trusted(ctx, root); err != nil || !ok {
		t.Fatalf("after Add: trusted=%v err=%v", ok, err)
	}

	// Remove by CN alone (certificate file lost), twice: idempotent.
	for i := range 2 {
		removed, err := k.Remove(ctx, "Benbow Local CA", nil)
		if err != nil {
			t.Fatalf("Remove #%d: %v", i+1, err)
		}
		if want := 1 - i; len(removed) != want {
			t.Fatalf("Remove #%d removed %d certs, want %d", i+1, len(removed), want)
		}
	}
	if _, ok := f.items[other.SHA1]; !ok {
		t.Fatal("Remove deleted a certificate with a different CN")
	}
	if ok, _ := k.Trusted(ctx, root); ok {
		t.Fatal("still trusted after Remove")
	}
}

func TestRemoveBySHA1WhenNotInKeychain(t *testing.T) {
	f := newFake()
	k := &Keychain{Path: "/tmp/login.keychain-db", Run: f.run}
	root := makeCert(t, "Benbow Local CA")
	// Trust settings exist but the keychain item was deleted by hand.
	f.trusted[root.SHA1] = root.CommonName

	if _, err := k.Remove(context.Background(), "Benbow Local CA", &root); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.trusted[root.SHA1]; ok {
		t.Fatal("trust settings not removed")
	}
}

func TestRemoveByCertLeavesOtherCAs(t *testing.T) {
	ctx := context.Background()
	f := newFake()
	k := &Keychain{Path: "/tmp/login.keychain-db", Run: f.run}
	mine := makeCert(t, "Benbow Local CA")
	theirs := makeCert(t, "Benbow Local CA") // e.g. another Benbow home
	for _, c := range []Cert{mine, theirs} {
		if err := k.Add(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := k.Remove(ctx, "Benbow Local CA", &mine)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || removed[0].SHA1 != mine.SHA1 {
		t.Fatalf("removed %v, want only mine", removed)
	}
	if ok, _ := k.Trusted(ctx, theirs); !ok {
		t.Fatal("removing by certificate touched another CA with the same name")
	}
}

func TestRemoveNothingRunsNoChange(t *testing.T) {
	f := newFake()
	k := &Keychain{Path: "/tmp/login.keychain-db", Run: f.run}
	root := makeCert(t, "Benbow Local CA")
	removed, err := k.Remove(context.Background(), "Benbow Local CA", &root)
	if err != nil || len(removed) != 0 {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	for _, c := range f.calls {
		if c != "find-certificate" && c != "dump-trust-settings" {
			t.Fatalf("ran %q with nothing to remove (could show a dialog)", c)
		}
	}
}

func TestHasTrustEntry(t *testing.T) {
	out := "Number of trusted certs = 2\nCert 0: MAMP_PRO_Root_CA\n   Number of trust settings : 1\nCert 1: Benbow Local CA\n"
	tests := []struct {
		cn   string
		want bool
	}{
		{"Benbow Local CA", true},
		{"MAMP_PRO_Root_CA", true},
		{"Benbow Local", false},
	}
	for _, tt := range tests {
		if got := hasTrustEntry(out, tt.cn); got != tt.want {
			t.Errorf("hasTrustEntry(%q) = %v, want %v", tt.cn, got, tt.want)
		}
	}
}
