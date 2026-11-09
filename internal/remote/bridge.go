// SPDX-License-Identifier: GPL-3.0-only
// Copyright (C) 2026 Andrea Casali

package remote

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/benbowdev/benbow/internal/errcode"
	"github.com/benbowdev/benbow/internal/version"
)

// The bridge reaches a site's database on hosts without SSH: a small PHP
// file with a random name, uploaded next to the site, that only answers
// requests carrying its 256-bit token, stops working after its expiry and
// deletes itself then. Benbow deletes it as soon as it is done, whatever
// happens. It reads the database settings from wp-config.php or .env on
// the server.

// BridgeTTL is how long a bridge works.
const BridgeTTL = 10 * time.Minute

// EnvBridgeCA trusts an extra CA for the bridge's HTTPS (tests).
const EnvBridgeCA = "BENBOW_BRIDGE_CA"

// Bridge is one uploaded bridge.
type Bridge struct {
	File  string // file name
	Token string
	URL   string
}

// NewBridge returns a bridge for a site at siteURL and its PHP source.
func NewBridge(siteURL string) (Bridge, string, error) {
	if err := checkBridgeURL(siteURL); err != nil {
		return Bridge{}, "", err
	}
	name := make([]byte, 8)
	token := make([]byte, 32)
	if _, err := rand.Read(name); err != nil {
		return Bridge{}, "", fmt.Errorf("random: %w", err)
	}
	if _, err := rand.Read(token); err != nil {
		return Bridge{}, "", fmt.Errorf("random: %w", err)
	}
	b := Bridge{File: "benbow-bridge-" + hex.EncodeToString(name) + ".php", Token: hex.EncodeToString(token)}
	b.URL = strings.TrimRight(siteURL, "/") + "/" + b.File
	src := strings.NewReplacer("BENBOW_TOKEN_VALUE", b.Token, "BENBOW_EXPIRES_VALUE", strconv.FormatInt(time.Now().Add(BridgeTTL).Unix(), 10)).Replace(bridgePHP)
	return b, src, nil
}

// checkBridgeURL refuses plain HTTP to another machine: the token and the
// whole database would cross the network in clear.
func checkBridgeURL(siteURL string) error {
	u, err := url.Parse(siteURL)
	if err != nil || u.Host == "" {
		return errcode.Newf(errcode.InvalidInput, "bad site address %q", siteURL)
	}
	if u.Scheme == "https" {
		return nil
	}
	host := strings.ToLower(u.Hostname())
	loopback := host == "localhost" || strings.HasSuffix(host, ".localhost")
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		loopback = true
	}
	if u.Scheme == "http" && loopback {
		return nil
	}
	return errcode.Newf(errcode.InvalidInput,
		"the database bridge needs an https:// address (%s would send the database in clear); set it with benbow remote set … --url https://…", siteURL)

}

func bridgeClient() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if ca := os.Getenv(EnvBridgeCA); ca != "" {
		if pem, err := os.ReadFile(ca); err == nil {
			pool, _ := x509.SystemCertPool()
			if pool == nil {
				pool = x509.NewCertPool()
			}
			pool.AppendCertsFromPEM(pem)
			tr.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		}
	}
	// Never follow a redirect: Go would send the token along, possibly to
	// another host or over plain HTTP.
	return &http.Client{Transport: version.Transport{Base: tr}, Timeout: 30 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func (b Bridge) request(ctx context.Context, method, action string, body io.Reader) (*http.Response, error) {
	return b.do(ctx, method, action, body, -1)
}

func (b Bridge) requestSized(ctx context.Context, action string, body io.Reader, size int64) (*http.Response, error) {
	return b.do(ctx, http.MethodPost, action, body, size)
}

func (b Bridge) do(ctx context.Context, method, action string, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, b.URL+"?a="+action, body)
	if err != nil {
		return nil, fmt.Errorf("bridge request: %w", err)
	}
	if size >= 0 {
		req.ContentLength = size
	}
	req.Header.Set("X-Benbow-Token", b.Token)
	resp, err := bridgeClient().Do(req)
	if err != nil {
		return nil, errcode.New(errcode.Service, fmt.Errorf("reach the bridge at %s: %w", b.URL, err))
	}
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		resp.Body.Close()
		return nil, errcode.Newf(errcode.Service,
			"the server redirects the bridge to %s; set the remote's address to the final one (benbow remote set … --url)", resp.Header.Get("Location"))

	}
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		resp.Body.Close()
		return nil, errcode.Newf(errcode.Service, "the bridge answered %s: %s (some hosts block new PHP files)", resp.Status, strings.TrimSpace(string(msg)))
	}
	return resp, nil
}

// BridgeInfo is what the bridge found on the server.
type BridgeInfo struct {
	Database string `json:"database"`
	Tables   int    `json:"tables"`
	Home     string `json:"home,omitempty"`
	PHP      string `json:"php"`
}

// Info asks the bridge about the database.
func (b Bridge) Info(ctx context.Context) (BridgeInfo, error) {
	var info BridgeInfo
	resp, err := b.request(ctx, http.MethodGet, "info", nil)
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return info, errcode.New(errcode.Service, fmt.Errorf("read the bridge's answer: %w", err))
	}
	return info, nil
}

// Dump streams the database as gzip-compressed SQL into w.
func (b Bridge) Dump(ctx context.Context, w io.Writer) error {
	resp, err := b.request(ctx, http.MethodGet, "dump", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if _, err := io.Copy(w, resp.Body); err != nil {
		return errcode.New(errcode.Service, fmt.Errorf("download the database: %w", err))
	}
	return nil
}

// Import sends gzip-compressed SQL of a known size for the bridge to run
// (hosts' FastCGI setups want a Content-Length).
func (b Bridge) Import(ctx context.Context, gz io.Reader, size int64) error {
	resp, err := b.requestSized(ctx, "import", gz, size)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4000))
	if strings.TrimSpace(string(out)) != "ok" {
		return errcode.Errorf(errcode.Service, "the bridge could not import: "+strings.TrimSpace(string(out)))
	}
	return nil
}

// Bye asks the bridge to delete itself (Benbow also deletes it by FTP/SFTP).
func (b Bridge) Bye(ctx context.Context) {
	if resp, err := b.request(ctx, http.MethodGet, "bye", nil); err == nil {
		resp.Body.Close()
	}
}

// bridgePHP needs PHP 5.6+, mysqli and zlib (both standard on hosts).
const bridgePHP = `<?php
// Benbow bridge: temporary. It deletes itself after use or after expiry.
@set_time_limit(0);
@ignore_user_abort(true);
$benbow_token = 'BENBOW_TOKEN_VALUE';
$benbow_expires = BENBOW_EXPIRES_VALUE;
if (time() > $benbow_expires) { @unlink(__FILE__); http_response_code(410); exit('expired'); }
$given = isset($_SERVER['HTTP_X_BENBOW_TOKEN']) ? $_SERVER['HTTP_X_BENBOW_TOKEN'] : '';
if (!is_string($given) || !hash_equals($benbow_token, $given)) { http_response_code(403); exit('forbidden'); }
$a = isset($_GET['a']) ? $_GET['a'] : '';
if ($a === 'bye') { @unlink(__FILE__); exit('bye'); }

function benbow_fail($msg) { http_response_code(500); exit($msg); }
function benbow_settings() {
	foreach (array(__DIR__, dirname(__DIR__)) as $dir) {
		$f = $dir . '/wp-config.php';
		if (is_file($f)) {
			$src = preg_replace('~^\s*(//|#).*$~m', '', file_get_contents($f));
			$v = array();
			foreach (array('DB_NAME', 'DB_USER', 'DB_PASSWORD', 'DB_HOST') as $k) {
				if (preg_match('~define\(\s*[\'"]' . $k . '[\'"]\s*,\s*(?:\'((?:\\\\.|[^\\\\\'])*)\'|"((?:\\\\.|[^\\\\"])*)")\s*\)~', $src, $m)) {
					$v[$k] = stripslashes(isset($m[2]) && $m[2] !== '' ? $m[2] : $m[1]);
				}
			}
			$prefix = preg_match('~\$table_prefix\s*=\s*[\'"]([A-Za-z0-9_]+)[\'"]~', $src, $m) ? $m[1] : 'wp_';
			return array($v['DB_HOST'], $v['DB_USER'], $v['DB_PASSWORD'], $v['DB_NAME'], $prefix);
		}
		$e = $dir . '/.env';
		if (is_file($e)) {
			$v = array();
			foreach (file($e) as $line) {
				if (preg_match('~^\s*([A-Z_]+)\s*=\s*(.*?)\s*$~', $line, $m)) { $v[$m[1]] = trim($m[2], "\"'"); }
			}
			$host = (isset($v['DB_HOST']) ? $v['DB_HOST'] : '127.0.0.1') . (isset($v['DB_PORT']) ? ':' . $v['DB_PORT'] : '');
			return array($host, $v['DB_USERNAME'], $v['DB_PASSWORD'], $v['DB_DATABASE'], '');
		}
	}
	benbow_fail('no wp-config.php or .env next to the bridge');
}
list($host, $user, $pass, $name, $prefix) = benbow_settings();
$port = null; $socket = null;
if (preg_match('~^(.*):(/.*)$~', $host, $m)) { $host = $m[1]; $socket = $m[2]; }
elseif (preg_match('~^(.*):(\d+)$~', $host, $m)) { $host = $m[1]; $port = (int) $m[2]; }
mysqli_report(MYSQLI_REPORT_OFF);
$db = @new mysqli($host, $user, $pass, $name, $port, $socket);
if ($db->connect_errno) { benbow_fail('database: ' . $db->connect_error); }
$db->set_charset('utf8mb4');

if ($a === 'info') {
	header('Content-Type: application/json');
	$r = $db->query("SHOW TABLES");
	$home = '';
	if ($prefix !== '' && ($h = $db->query("SELECT option_value FROM ` + "`" + `{$prefix}options` + "`" + ` WHERE option_name = 'home'"))) {
		$row = $h->fetch_row(); $home = $row ? $row[0] : '';
	}
	exit(json_encode(array('database' => $name, 'tables' => $r ? $r->num_rows : 0, 'home' => $home, 'php' => PHP_VERSION)));
}

if ($a === 'dump') {
	header('Content-Type: application/gzip');
	while (ob_get_level()) { ob_end_clean(); }
	$z = deflate_init(ZLIB_ENCODING_GZIP);
	$out = function ($s, $end = false) use ($z) { echo deflate_add($z, $s, $end ? ZLIB_FINISH : ZLIB_NO_FLUSH); flush(); };
	$out("SET SESSION sql_mode='NO_AUTO_VALUE_ON_ZERO';\nSET NAMES utf8mb4;\nSET FOREIGN_KEY_CHECKS=0;\n");
	$tables = array(); $views = array();
	$r = $db->query("SHOW FULL TABLES");
	while ($row = $r->fetch_row()) { if ($row[1] === 'VIEW') { $views[] = $row[0]; } else { $tables[] = $row[0]; } }
	foreach ($tables as $t) {
		$q = '` + "`" + `' . str_replace('` + "`" + `', '` + "``" + `', $t) . '` + "`" + `';
		$c = $db->query("SHOW CREATE TABLE $q")->fetch_row();
		$out("DROP TABLE IF EXISTS $q;\n" . str_replace("\n", " ", $c[1]) . ";\n");
		$res = $db->query("SELECT * FROM $q", MYSQLI_USE_RESULT);
		$batch = array();
		while ($row = $res->fetch_row()) {
			$vals = array();
			foreach ($row as $v) { $vals[] = $v === null ? 'NULL' : "'" . $db->real_escape_string($v) . "'"; }
			$batch[] = '(' . implode(',', $vals) . ')';
			if (count($batch) >= 200) { $out("INSERT INTO $q VALUES " . implode(',', $batch) . ";\n"); $batch = array(); }
		}
		$res->free();
		if ($batch) { $out("INSERT INTO $q VALUES " . implode(',', $batch) . ";\n"); }
	}
	foreach ($views as $t) {
		$q = '` + "`" + `' . str_replace('` + "`" + `', '` + "``" + `', $t) . '` + "`" + `';
		$c = $db->query("SHOW CREATE VIEW $q")->fetch_row();
		$out("DROP VIEW IF EXISTS $q;\n" . str_replace("\n", " ", $c[1]) . ";\n");
	}
	$out("SET FOREIGN_KEY_CHECKS=1;\n", true);
	exit;
}

if ($a === 'import' && $_SERVER['REQUEST_METHOD'] === 'POST') {
	$in = fopen('php://input', 'rb');
	$z = inflate_init(ZLIB_ENCODING_GZIP);
	$db->query("SET SESSION sql_mode='NO_AUTO_VALUE_ON_ZERO'");
	$buf = ''; $stmt = '';
	$run = function ($line) use ($db, &$stmt) {
		$t = rtrim($line, "\r\n");
		if ($stmt === '' && ($t === '' || strpos($t, '--') === 0)) { return; }
		$stmt .= $line;
		if (substr($t, -1) === ';') {
			if (!$db->query($stmt)) { benbow_fail('SQL error: ' . $db->error); }
			$stmt = '';
		}
	};
	while (!feof($in)) {
		$buf .= inflate_add($z, fread($in, 1 << 20));
		while (($p = strpos($buf, "\n")) !== false) { $run(substr($buf, 0, $p + 1)); $buf = substr($buf, $p + 1); }
	}
	$buf .= inflate_add($z, '', ZLIB_FINISH);
	if ($buf !== '') { $run($buf . "\n"); }
	exit('ok');
}
benbow_fail('unknown action');
`
