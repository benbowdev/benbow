// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

//go:build integration

package e2e

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/benbowdev/benbow/internal/ca"
)

// startedSite adds a site with index.php body and starts Benbow. It returns
// the site URL and the CA directory.
func startedSite(t *testing.T, h *harness, php string) (string, ca.Dir) {
	t.Helper()
	dir := newSite(t)
	if err := os.WriteFile(filepath.Join(dir, "index.php"), []byte(php), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ok.txt"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var added struct {
		URL string `json:"url"`
	}
	if err := json.Unmarshal(h.mustBenbow("site", "add", "demo", "--php", "8.4", "--path", dir), &added); err != nil {
		t.Fatal(err)
	}
	h.mustBenbow("start")
	var status struct {
		CA struct {
			CertPath string `json:"cert_path"`
		} `json:"ca"`
	}
	if err := json.Unmarshal(h.mustBenbow("status"), &status); err != nil {
		t.Fatal(err)
	}
	return added.URL, ca.Dir(filepath.Dir(status.CA.CertPath))
}

func TestCAIsConstrained(t *testing.T) {
	h := newHarness(t)
	url, dir := startedSite(t, h, "<?php echo 'hi';")
	h.get(url, dir.RootCert())

	// The chain Caddy serves: leaf + Benbow's intermediate.
	conn, err := tls.Dial("tcp", "127.0.0.1:"+strconv.Itoa(h.port), &tls.Config{
		ServerName: "demo.localhost", InsecureSkipVerify: true, //nolint:gosec // inspected below
	})
	if err != nil {
		t.Fatal(err)
	}
	chain := conn.ConnectionState().PeerCertificates
	_ = conn.Close()
	if len(chain) != 2 {
		t.Fatalf("served chain has %d certificates, want leaf + intermediate", len(chain))
	}
	m, err := ca.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	rb, _ := pem.Decode(m.RootCert)
	root, err := x509.ParseCertificate(rb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Check(m, root.Subject.CommonName, time.Now()); err != nil {
		t.Fatalf("CA on disk: %v", err)
	}
	if !strings.HasPrefix(root.Subject.CommonName, "Benbow Test CA ") {
		t.Fatalf("root is named %q: tests must never create the real CA", root.Subject.CommonName)
	}
	if !ca.Constrained(chain[1]) {
		t.Fatal("served intermediate is not name-constrained")
	}
	if b, _ := pem.Decode(m.IntermediateCert); b == nil || string(b.Bytes) != string(chain[1].Raw) {
		t.Fatal("Caddy does not serve Benbow's intermediate")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(string(dir)), "caddy", "pki")); err == nil {
		t.Fatal("Caddy created its own PKI")
	}
	if _, err := os.Stat(filepath.Join(string(dir), "root.key")); err == nil {
		t.Fatal("the root key must never be stored")
	}

	// macOS (the verifier Safari uses) with Benbow's root as the only anchor,
	// without touching any keychain.
	tmp := t.TempDir()
	write := func(name string, der []byte) string {
		p := filepath.Join(tmp, name)
		if err := os.WriteFile(p, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	inter := write("inter.pem", chain[1].Raw)
	verify := func(leaf string) error {
		out, err := exec.Command("/usr/bin/security", "verify-cert",
			"-c", leaf, "-c", inter, "-r", dir.RootCert(), "-p", "basic", "-L", "-n").CombinedOutput()
		if err != nil {
			return &statusError{1, string(out)}
		}
		return nil
	}
	if err := verify(write("served.pem", chain[0].Raw)); err != nil {
		t.Fatalf("macOS rejects the served certificate: %v", err)
	}
	for _, name := range []string{"example.com", "localhost.example.com", "127.0.0.1"} {
		if err := verify(write(name+".pem", signLeaf(t, m, name))); err == nil {
			t.Errorf("macOS accepts a certificate for %s signed by Benbow's intermediate", name)
		}
	}
}

// signLeaf issues a certificate for name with Benbow's intermediate, as an
// attacker holding the intermediate key could.
func signLeaf(t *testing.T, m ca.Material, name string) []byte {
	t.Helper()
	ib, _ := pem.Decode(m.IntermediateCert)
	inter, err := x509.ParseCertificate(ib.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := pem.Decode(m.IntermediateKey)
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	lk, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: name},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
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
	return der
}

// TestPHPTrustsCA checks that a site can call itself over HTTPS from PHP,
// as WordPress does, with certificate verification on.
func TestPHPTrustsCA(t *testing.T) {
	h := newHarness(t)
	url, dir := startedSite(t, h, `<?php
$u = 'https://' . $_SERVER['HTTP_HOST'] . '/ok.txt';
$fgc = @file_get_contents($u);
$ch = curl_init($u);
curl_setopt($ch, CURLOPT_RETURNTRANSFER, true);
curl_setopt($ch, CURLOPT_SSL_VERIFYPEER, true);
curl_setopt($ch, CURLOPT_SSL_VERIFYHOST, 2);
$curl = curl_exec($ch);
echo json_encode([
	'fgc' => $fgc, 'fgc_error' => error_get_last()['message'] ?? '',
	'curl' => $curl, 'curl_error' => curl_error($ch),
	'cafile' => ini_get('openssl.cafile'),
]);`)
	body := h.get(url, dir.RootCert())
	var got struct {
		FGC       any    `json:"fgc"`
		FGCError  string `json:"fgc_error"`
		Curl      any    `json:"curl"`
		CurlError string `json:"curl_error"`
		CAFile    string `json:"cafile"`
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("bad response %q: %v", body, err)
	}
	if got.FGC != "ok\n" {
		t.Errorf("file_get_contents = %v (%s)", got.FGC, got.FGCError)
	}
	if got.Curl != "ok\n" {
		t.Errorf("curl = %v (%s)", got.Curl, got.CurlError)
	}
	if !strings.HasSuffix(got.CAFile, "ca-bundle.pem") {
		t.Errorf("openssl.cafile = %q", got.CAFile)
	}
	bundle, err := os.ReadFile(got.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(bundle), "BEGIN CERTIFICATE"); n < 100 {
		t.Errorf("bundle has %d certificates, want Mozilla's roots plus Benbow's", n)
	}
}
