// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"html"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/getcovo/covo/internal/caddy"
	"github.com/getcovo/covo/internal/errcode"
	"github.com/getcovo/covo/internal/fsutil"
	"github.com/getcovo/covo/internal/launchd"
	"github.com/getcovo/covo/internal/ports"
	"github.com/getcovo/covo/internal/store"
)

// Open on phone: one site at a time is also served on the Mac's private
// network addresses, under <site>-<mac>.local (published with Bonjour by
// /usr/bin/dns-sd), with a certificate from Covo's CA, whose Name
// Constraints already allow .local. It turns itself off after a while. A
// plain-HTTP page on the same addresses helps install the CA on the phone.
// The site's database and files are never changed: a PHP file of Covo's,
// prepended only to the phone's requests, shows the site under the phone
// address (WordPress) and Laravel gets APP_URL from the environment.

const (
	settingPhone        = "phone_share"
	phoneService        = "phone"
	phoneDefaultMinutes = 30
	phoneMaxMinutes     = 24 * 60
	phoneFirstPort      = 9443
)

// PhoneShare is the site open on the phone.
type PhoneShare struct {
	Site  string    `json:"site"`
	Host  string    `json:"host"`
	Port  int       `json:"port"`
	Until time.Time `json:"until"`
	// CAPort serves the CA install page over plain HTTP.
	CAPort int `json:"ca_port"`
	// PlainPort serves the site over plain HTTP on the private addresses:
	// nothing to install on the phone.
	PlainPort int `json:"plain_port,omitempty"`
}

// PhoneStatus is what the app shows while a site is open on the phone.
type PhoneStatus struct {
	PhoneShare
	URL string `json:"url"`
	// PlainURL opens the site at once, over plain HTTP on the Mac's
	// address (no certificate, no .local name needed).
	PlainURL string `json:"plain_url,omitempty"`
	// CAPageURLs are the CA install page on each private address.
	CAPageURL string   `json:"ca_page_url"`
	Addresses []string `json:"addresses"`
	// Firewall is "off", "on" or "block-all" (macOS application firewall).
	Firewall string `json:"firewall"`
	Minutes  int    `json:"minutes_left"`
}

// privateAddresses are the Mac's IPv4 addresses on private networks
// (RFC 1918) of interfaces that are up, never loopback or public ones.
func privateAddresses() []string {
	var out []string
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			out = append(out, ipn.IP.String())
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

var labelClean = regexp.MustCompile(`[^a-z0-9-]+`)

// macName is the Mac's Bonjour name ("Andreas-MacBook-Pro") as a DNS label.
func macName() string {
	name := ""
	if out, err := exec.Command("/usr/sbin/scutil", "--get", "LocalHostName").Output(); err == nil {
		name = strings.TrimSpace(string(out))
	}
	if name == "" {
		name, _ = os.Hostname()
		name = strings.TrimSuffix(name, ".local")
	}
	name = strings.Trim(labelClean.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if name == "" {
		name = "mac"
	}
	return name
}

// phoneHost is <site>-<mac>.local, one label of at most 63 characters.
func phoneHost(site, mac string) string {
	label := site + "-" + mac
	if len(label) > 63 {
		label = strings.TrimRight(label[:63], "-")
	}
	return label + "." + suffixLocal
}

// firewallState reads the macOS application firewall (read only).
func firewallState() string {
	out, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getglobalstate").Output()
	if err != nil {
		return "unknown"
	}
	if !strings.Contains(string(out), "enabled") {
		return "off"
	}
	if b, err := exec.Command("/usr/libexec/ApplicationFirewall/socketfilterfw", "--getblockall").Output(); err == nil && strings.Contains(string(b), "enabled") {
		return "block-all"
	}
	return "on"
}

func (c *Core) phoneShare(ctx context.Context) (*PhoneShare, error) {
	v, ok, err := c.Store.Setting(ctx, settingPhone)
	if err != nil || !ok {
		return nil, err
	}
	var s PhoneShare
	if err := json.Unmarshal([]byte(v), &s); err != nil {
		return nil, fmt.Errorf("read phone share: %w", err)
	}
	if time.Now().After(s.Until) {
		return nil, nil
	}
	return &s, nil
}

// SharePhone opens a site on the phone for minutes (default 30), replacing
// any site shared before. Covo must be running.
func (c *Core) SharePhone(ctx context.Context, name string, minutes int) (PhoneStatus, error) {
	if minutes == 0 {
		minutes = phoneDefaultMinutes
	}
	if minutes < 1 || minutes > phoneMaxMinutes {
		return PhoneStatus{}, errcode.Errorf(errcode.InvalidInput, fmt.Sprintf("minutes must be between 1 and %d", phoneMaxMinutes))
	}
	var res PhoneStatus
	err := c.locked(ctx, func() error {
		site, err := c.Store.Site(ctx, name)
		if err != nil {
			return err
		}
		if !site.Enabled {
			return errcode.Errorf(errcode.Conflict, fmt.Sprintf("%s is off: turn it on first", name))
		}
		if _, running, err := c.Store.Setting(ctx, settingRunning); err != nil {
			return err
		} else if !running {
			return errcode.Errorf(errcode.Conflict, "Covo is stopped: start it first")
		}
		if len(privateAddresses()) == 0 {
			return errcode.Errorf(errcode.Unsupported, "this Mac is on no private network (Wi-Fi or Ethernet with a 192.168.x.x, 10.x.x.x or 172.16–31.x.x address)")
		}
		old, err := c.phoneShare(ctx)
		if err != nil {
			return err
		}
		share := PhoneShare{Site: name, Host: phoneHost(name, macName()), Until: time.Now().Add(time.Duration(minutes) * time.Minute)}
		if old != nil {
			share.Port, share.CAPort, share.PlainPort = old.Port, old.CAPort, old.PlainPort
		} else {
			if share.Port, err = firstFreeAnywhere(phoneFirstPort); err != nil {
				return err
			}
			if share.CAPort, err = firstFreeAnywhere(share.Port + 1); err != nil {
				return err
			}
		}
		if share.PlainPort == 0 {
			if share.PlainPort, err = firstFreeAnywhere(max(share.Port, share.CAPort) + 1); err != nil {
				return err
			}
		}
		b, _ := json.Marshal(share)
		if err := c.Store.SetSetting(ctx, settingPhone, string(b)); err != nil {
			return err
		}
		if _, err := c.reconcile(ctx); err != nil {
			_ = c.Store.DeleteSetting(ctx, settingPhone)
			_, _ = c.reconcile(ctx)
			return err
		}
		res, err = c.phoneStatus(ctx, &share)
		return err
	})
	return res, err
}

func firstFreeAnywhere(from int) (int, error) {
	for p := from; p < from+portSearchSpan; p++ {
		if ports.FreeAnywhere(p) {
			return p, nil
		}
	}
	return 0, errcode.Errorf(errcode.PortInUse, fmt.Sprintf("no free port from %d", from))
}

// StopPhone closes the phone access.
func (c *Core) StopPhone(ctx context.Context) error {
	return c.locked(ctx, func() error {
		if _, ok, err := c.Store.Setting(ctx, settingPhone); err != nil || !ok {
			return err
		}
		if err := c.Store.DeleteSetting(ctx, settingPhone); err != nil {
			return err
		}
		_, err := c.applyIfRunning(ctx)
		return err
	})
}

// ExpirePhone turns phone access off once its time is up (the daemon calls
// it every minute). It reports whether it did.
func (c *Core) ExpirePhone(ctx context.Context) (bool, error) {
	v, ok, err := c.Store.Setting(ctx, settingPhone)
	if err != nil || !ok {
		return false, err
	}
	var s PhoneShare
	if json.Unmarshal([]byte(v), &s) == nil && time.Now().Before(s.Until) {
		return false, nil
	}
	return true, c.StopPhone(ctx)
}

// Phone returns the site open on the phone, or nil.
func (c *Core) Phone(ctx context.Context) (*PhoneStatus, error) {
	s, err := c.phoneShare(ctx)
	if err != nil || s == nil {
		return nil, err
	}
	st, err := c.phoneStatus(ctx, s)
	return &st, err
}

func (c *Core) phoneStatus(_ context.Context, s *PhoneShare) (PhoneStatus, error) {
	addrs := privateAddresses()
	st := PhoneStatus{PhoneShare: *s, Addresses: addrs, Firewall: firewallState(),
		URL:     "https://" + s.Host + ":" + strconv.Itoa(s.Port),
		Minutes: max(0, int(time.Until(s.Until).Round(time.Minute).Minutes())),
	}
	if len(addrs) > 0 {
		st.CAPageURL = "http://" + addrs[0] + ":" + strconv.Itoa(s.CAPort) + "/"
		if s.PlainPort != 0 {
			st.PlainURL = plainURL(addrs[0], s.PlainPort)
		}
	}
	return st, nil
}

// phoneConfig returns how Caddy and launchd serve the shared site, or nil.
// It writes the prepend file and the CA page.
func (c *Core) phoneConfig(ctx context.Context, sites []store.Site, publicHTTPS int) (*caddy.Phone, []launchd.Agent, error) {
	s, err := c.phoneShare(ctx)
	if err != nil || s == nil {
		return nil, nil, err
	}
	var site *store.Site
	for i := range sites {
		if sites[i].Name == s.Site {
			site = &sites[i]
		}
	}
	addrs := privateAddresses()
	if site == nil || len(addrs) == 0 {
		return nil, nil, nil
	}
	dir := filepath.Join(c.Layout.Data, "phone")
	prepend := filepath.Join(dir, "prepend.php")
	if _, err := fsutil.WriteAtomic(prepend, []byte(phonePrepend), 0o644); err != nil {
		return nil, nil, err
	}
	if err := c.writeCAPage(filepath.Join(dir, "ca"), s); err != nil {
		return nil, nil, err
	}
	settings, err := c.sitePHPSettings(ctx, *site)
	if err != nil {
		return nil, nil, err
	}
	phoneURL := "https://" + s.Host + ":" + strconv.Itoa(s.Port)
	cfg := &caddy.Phone{
		Site: site.Name, Host: s.Host, Port: s.Port, Binds: addrs,
		Root: site.Path, FPMSocket: c.Layout.Socket(socketName(site.PHPMinor)),
		PHPValue: settings.PHPValue(c.siteLogPath(site.Name, LogPHP)) + "\nauto_prepend_file=" + prepend,
		LocalURL: siteURL(*site, publicHTTPS), PhoneURL: phoneURL,
		Laravel: site.Framework == FrameworkLaravel, CAPort: s.CAPort, CADir: filepath.Join(dir, "ca"),
	}
	if s.PlainPort != 0 {
		cfg.PlainPort, cfg.PlainURL = s.PlainPort, plainURL(addrs[0], s.PlainPort)
	}
	agent := launchd.Agent{
		Label:   c.labelFor(phoneService),
		Program: "/usr/bin/dns-sd",
		// A proxy registration: <host>.local → the first private address,
		// published by mDNSResponder while this runs.
		Args:      []string{"-P", "Covo " + site.Name, "_https._tcp", "local", strconv.Itoa(s.Port), s.Host, addrs[0]},
		WorkDir:   c.Layout.Root,
		StdoutLog: "/dev/null",
		StderrLog: "/dev/null",
	}
	return cfg, []launchd.Agent{agent}, nil
}

func plainURL(addr string, port int) string { return "http://" + addr + ":" + strconv.Itoa(port) }

// phonePrepend runs before the site's PHP, only for the phone's requests
// (Caddy sends COVO_PHONE_URL there only): WordPress sees the phone address
// as its home, and pages have the local address replaced. Nothing is
// written anywhere.
const phonePrepend = `<?php
// Generated by Covo for "Open on phone". Do not edit.
if (!empty($_SERVER['COVO_PHONE_URL']) && !empty($_SERVER['COVO_LOCAL_URL'])) {
	$covo_phone = $_SERVER['COVO_PHONE_URL'];
	$covo_local = $_SERVER['COVO_LOCAL_URL'];
	if (!defined('WP_HOME')) { define('WP_HOME', $covo_phone); }
	if (!defined('WP_SITEURL')) { define('WP_SITEURL', $covo_phone); }
	ob_start(function ($out) use ($covo_phone, $covo_local) {
		return str_replace(
			[$covo_local, str_replace('/', '\/', $covo_local)],
			[$covo_phone, str_replace('/', '\/', $covo_phone)],
			$out
		);
	});
}
`

// writeCAPage writes the plain-HTTP page that installs Covo's CA on a
// phone: the certificate, an iOS profile and the instructions.
func (c *Core) writeCAPage(dir string, s *PhoneShare) error {
	pemBytes, err := os.ReadFile(c.CACertPath())
	if err != nil {
		return fmt.Errorf("read Covo's CA: %w", err)
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return errcode.Errorf(errcode.Internal, "Covo's CA certificate is not PEM")
	}
	sum := sha256.Sum256(block.Bytes)
	files := map[string][]byte{
		"covo-ca.crt":          pemBytes,
		"covo-ca.mobileconfig": []byte(mobileconfig(block.Bytes, hex.EncodeToString(sum[:]))),
		"index.html":           []byte(caPageHTML(s, hex.EncodeToString(sum[:]))),
	}
	for name, body := range files {
		if _, err := fsutil.WriteAtomic(filepath.Join(dir, name), body, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// mobileconfig is an iOS configuration profile with Covo's root.
func mobileconfig(der []byte, fingerprint string) string {
	uuid := func(salt string) string {
		h := sha256.Sum256([]byte(salt + fingerprint))
		x := hex.EncodeToString(h[:16])
		return strings.ToUpper(x[0:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:32])
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>PayloadContent</key>
	<array>
		<dict>
			<key>PayloadCertificateFileName</key>
			<string>covo-ca.cer</string>
			<key>PayloadContent</key>
			<data>` + base64.StdEncoding.EncodeToString(der) + `</data>
			<key>PayloadDescription</key>
			<string>Covo's local certificate authority, limited to .localhost, .test and .local names.</string>
			<key>PayloadDisplayName</key>
			<string>Covo Local CA</string>
			<key>PayloadIdentifier</key>
			<string>dev.getcovo.ca.` + fingerprint[:16] + `</string>
			<key>PayloadType</key>
			<string>com.apple.security.root</string>
			<key>PayloadUUID</key>
			<string>` + uuid("cert") + `</string>
			<key>PayloadVersion</key>
			<integer>1</integer>
		</dict>
	</array>
	<key>PayloadDisplayName</key>
	<string>Covo Local CA</string>
	<key>PayloadIdentifier</key>
	<string>dev.getcovo.profile.` + fingerprint[:16] + `</string>
	<key>PayloadRemovalDisallowed</key>
	<false/>
	<key>PayloadType</key>
	<string>Configuration</string>
	<key>PayloadUUID</key>
	<string>` + uuid("profile") + `</string>
	<key>PayloadVersion</key>
	<integer>1</integer>
</dict>
</plist>
`
}

func caPageHTML(s *PhoneShare, fingerprint string) string {
	url := html.EscapeString("https://" + s.Host + ":" + strconv.Itoa(s.Port))
	return `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Covo on your phone</title>
<style>body{font:17px/1.5 -apple-system,system-ui,sans-serif;max-width:34em;margin:2em auto;padding:0 1em;color:#222}
a.b{display:block;text-align:center;background:#2563eb;color:#fff;padding:.8em;border-radius:.6em;text-decoration:none;margin:.8em 0}
code{font-size:.8em;word-break:break-all}@media(prefers-color-scheme:dark){body{background:#111;color:#eee}}</style></head>
<body>
<h1>Open ` + html.EscapeString(s.Site) + ` on this phone</h1>
<p>So that this phone trusts the site's HTTPS certificate, install Covo's local certificate authority once.</p>
<h2>iPhone and iPad</h2>
<a class="b" href="covo-ca.mobileconfig">Download the profile</a>
<ol><li>Settings → Profile Downloaded → Install.</li>
<li>Settings → General → About → Certificate Trust Settings → turn on “Covo Local CA”.</li></ol>
<h2>Android</h2>
<a class="b" href="covo-ca.crt">Download the certificate</a>
<p>Settings → Security → Encryption &amp; credentials → Install a certificate → CA certificate. Android may not find <code>.local</code> names: if the site does not open, use an iPhone or a computer.</p>
<h2>Then</h2>
<a class="b" href="` + url + `">Open ` + url + `</a>
<h2>Is it safe?</h2>
<p>Covo's certificate authority can only vouch for <b>.localhost</b>, <b>.test</b> and <b>.local</b> names (X.509 Name Constraints): even if its key were stolen, it could not be used for any real website. Its fingerprint (SHA-256) is <code>` + fingerprint + `</code>; compare it with Covo's on your Mac. You can remove it at any time from the same settings.</p>
<p>This page and the site are served only on your local network, only while “Open on phone” is on in Covo.</p>
</body></html>
`
}
