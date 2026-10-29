# Spike notes

Decisions, risks and findings from the Spike phase. Short on purpose.

## Components

- **PHP**: static-php-cli prebuilt "bulk" FPM builds from `dl.static-php.dev`
  (8.4.23, 8.2.32; arm64 and amd64). The archive contains a single `php-fpm`
  binary, ad-hoc signed, linked only against system libraries.
- **Extensions**: everything WordPress needs is built in (curl, dom, exif,
  fileinfo, gd, imagick, intl, mbstring, mysqli, openssl, sodium, xml, zip,
  OPcache). Checked by `TestPHPInstallModules` (`go test -tags integration`).
- **Caddy**: official GitHub release archives.

## Checksums: trust on first use

`dl.static-php.dev` publishes no checksums. The SHA-256 of each archive is
computed by us when pinning and committed in
`internal/components/manifest.json`, which is embedded in the binary. Trust is
therefore "on first use" at pin time: if the upstream file had already been
tampered with when we pinned it, we would not notice. Mitigations later: our
own build pipeline (v2), or cross-checking against static-php-cli's GitHub
Actions artifacts.

For Caddy the manifest also stores the SHA-512 from the official
`caddy_<v>_checksums.txt`; both hashes are verified at install time.

To pin a new version: download the archive, `shasum -a 256 <file>`, add the
entry to the manifest, run `go test ./internal/components/` and
`go test -tags integration ./test/e2e/ -run PHP`.

Nothing is extracted or executed before the hash matches. Only the files listed
in the manifest are extracted; symlinks, duplicates and oversized entries are
rejected. Installs are staged in `tmp/` and moved into `bin/` with a rename.

## Xdebug

Not included in static-php-cli builds. The binaries report
`Dynamic Library Support => enabled` and export Zend symbols, so a separately
built `xdebug.so` (NTS, matching API version, e.g. 20240924 for 8.4) should
load via `zend_extension=`. Needs a per-minor Xdebug build (static-php-cli can
build shared extensions) and a test before Core v1 promises the per-site
Xdebug toggle. **Risk: open.**

## Sockets

PHP-FPM and the Caddy admin API listen on Unix sockets, so the only TCP port
Covo opens is HTTPS (8443). Socket paths are limited to 103 bytes on macOS; if
`<COVO_HOME>/run/caddy-admin.sock` is longer, sockets move to
`$TMPDIR/covo-<uid>-<hash>/` (0700, owner checked).

## Services

- launchd user agents (`dev.getcovo.caddy`, `dev.getcovo.php84`, …) are
  bootstrapped from plists inside the Covo home, never copied to
  `~/Library/LaunchAgents`. Consequence: after a reboot nothing runs until
  `covo start`. Autostart at login will be an explicit option later.
- Caddy listens only on `127.0.0.1` and `[::1]` (`default_bind`) and only on
  the HTTPS port; it binds nothing while there are no sites. HTTP redirects
  are disabled, so port 80 (MAMP here) is never touched.
- Adding/removing a site reloads Caddy through its admin socket (no restart).
  A php.ini / php-fpm.conf change restarts that FPM agent (the config hash is
  part of the plist).
- **macOS privacy (TCC): tested 2026-10-29, macOS 26.2.** A site in
  `~/Documents/covo-tcc-test` served both a static file and `index.php`
  (HTTP 200), with no prompt and nothing in the logs. Caddy and PHP-FPM never
  got a permission of their own, while the shell running `covo start` (inside
  VS Code) can read `~/Documents` but has no Full Disk Access. So macOS
  attributes the agents to the app that ran `launchctl bootstrap`, and they
  inherit its folder permissions.
  Consequences: from a terminal app that has (or is granted) access to
  Documents/Desktop/Downloads/iCloud Drive, sites there work. From one that
  has been denied, Caddy/PHP would get "operation not permitted". The future
  Covo.app will be the responsible app and macOS will prompt for it once.
  No `protected_folder` warning was added since the read succeeded; revisit if
  a user reports the failure or when the GUI starts the agents.
