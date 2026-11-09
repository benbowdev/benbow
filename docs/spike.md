# Spike notes

Decisions, risks and findings from the Spike phase. Short on purpose.

## Result

`benbow site add demo --php 8.4 && benbow start` serves https://demo.localhost:8443
through Caddy + static PHP-FPM 8.4 with a trusted certificate, and a second
site on PHP 8.2 runs in parallel (`TestTwoPHPVersionsInParallel`).
`benbow uninstall` removes everything Benbow added: after running it on the real
home, the Step 0 inventory was identical (ports, launchd, keychain trust
settings, `/etc/resolver`, `/etc/hosts`), MAMP still running, site folder
untouched.

## Decisions

- **No daemon in the spike.** The CLI calls `internal/core` in-process. The
  core already returns plain, JSON-serialisable types, so Core v1 can put it
  behind the Unix-socket API without moving logic. Concurrent CLI runs are
  serialised with a `flock` on `benbow.lock`.
- **Embedded manifest** of pinned artifacts (see Checksums). Updating a
  component means a new Benbow build for now; Core v1 adds remote manifests.
- **Everything under the Benbow home**, launchd plists included. The only
  things outside it: the socket fallback in `$TMPDIR` (only when the home
  path is too long) and the CA in the login keychain after `benbow trust`.
- **Every command supports `--json`** with the envelope
  `{"schema":1,"ok":…,"data":…|"error":{"code","message"}}` and a non-zero
  exit code on error. Error codes are stable (`internal/errcode`).
- **Test isolation**: integration tests use their own `BENBOW_HOME`,
  `BENBOW_LABEL_PREFIX`, `BENBOW_HTTPS_PORT` and `BENBOW_CA_NAME`, so they never
  touch a real installation, its agents or its trusted CA.

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

## Site folders

`site add` refuses (`forbidden_folder`, even with `--force`): `/`, the home
folder, `/Users`, `/Volumes` and disk roots, system trees (`/System`,
`/Library`, `/usr`, `/etc`, `/Applications`, `~/Library`, …) and anything
overlapping the Benbow home (it holds the CA keys). Symlinks are resolved
first. A folder without `index.php` or `index.html` needs confirmation
(interactive) or `--force` (`confirmation_required`).

## Uninstall

`benbow uninstall` stops every agent under the label prefix, removes this
install's CA from the keychain (by SHA-1; by name only if the CA file is
gone), and deletes the Benbow home and socket fallback. It does not open the
database, so it also cleans a partial or corrupted install. It refuses to
delete a home containing files Benbow did not create (`conflict`) and asks for
confirmation unless `--yes`. Site folders are never touched. It also removes every
secret Benbow keeps in the login keychain, the `/etc/resolver` file and the
`.local` helper, and Benbow's MCP server from the AI agents it was connected
to. Before asking, it lists the backups of `wp-config.php`/`.env` taken by
migrations, which go with the Benbow home.

## Logs

Logs live in `logs/`. Caddy rolls `caddy.log` itself (10 MiB × 3). On every
start, any other log over 10 MiB is moved to `<name>.1`; PHP-FPM masters
that keep running get SIGUSR1 to reopen their log. launchd's stdout/stderr
files are only reopened when the agent restarts (both services write almost
nothing there).

## Sockets

PHP-FPM and the Caddy admin API listen on Unix sockets, so the only TCP port
Benbow opens is HTTPS (8443). Socket paths are limited to 103 bytes on macOS; if
`<BENBOW_HOME>/run/caddy-admin.sock` is longer, sockets move to
`$TMPDIR/benbow-<uid>-<hash>/` (0700, owner checked).

## Services

- launchd user agents (`dev.benbow.caddy`, `dev.benbow.php84`, …) are
  bootstrapped from plists inside the Benbow home, never copied to
  `~/Library/LaunchAgents`. Consequence: after a reboot nothing runs until
  `benbow start`. Autostart at login will be an explicit option later.
- Caddy listens only on `127.0.0.1` and `[::1]` (`default_bind`) and only on
  the HTTPS port; it binds nothing while there are no sites. HTTP redirects
  are disabled, so port 80 (MAMP here) is never touched.
- Adding/removing a site reloads Caddy through its admin socket (no restart).
  A php.ini / php-fpm.conf change restarts that FPM agent (the config hash is
  part of the plist).
- **macOS privacy (TCC): tested 2026-10-29, macOS 26.2.** A site in
  `~/Documents/benbow-tcc-test` served both a static file and `index.php`
  (HTTP 200), with no prompt and nothing in the logs. Caddy and PHP-FPM never
  got a permission of their own, while the shell running `benbow start` (inside
  VS Code) can read `~/Documents` but has no Full Disk Access. So macOS
  attributes the agents to the app that ran `launchctl bootstrap`, and they
  inherit its folder permissions.
  Consequences: from a terminal app that has (or is granted) access to
  Documents/Desktop/Downloads/iCloud Drive, sites there work. From one that
  has been denied, Caddy/PHP would get "operation not permitted". The future
  Benbow.app will be the responsible app and macOS will prompt for it once.
  No `protected_folder` warning was added since the read succeeded; revisit if
  a user reports the failure or when the GUI starts the agents.

## Local CA trust

- `benbow trust` runs `security add-trusted-cert -r trustRoot -k
  ~/Library/Keychains/login.keychain-db root.crt`: no sudo, trust for the
  current user only, one macOS authorization dialog. Caddy is configured with
  `skip_install_trust`, so it never tries to install its CA by itself.
- `benbow untrust` removes trust settings and keychain items for every
  certificate whose CN is exactly "Benbow Local CA" (the on-disk root is also
  matched by SHA-1). It only runs commands for items that are present, so a
  no-op never shows a dialog. Both commands are idempotent.
- Tested 2026-10-29 on the real home: trust → system `curl` and Chrome load
  https://demo.localhost:8443 with no warning; untrust → curl rejects it and
  only MAMP's root is left in the user trust settings; trust again → OK.
  Safari and Firefox: no warning either (checked by hand).
- `security verify-cert -p ssl` reports missing SCTs for the leaf. That is
  Apple's Certificate Transparency rule for publicly trusted CAs; user-added
  roots are exempt, as curl and Chrome confirm.
- The CA has no name constraints: once trusted, it could sign a certificate
  for any domain. Its key lives in `data/caddy/pki` (0700/0600) and never
  leaves the Mac. Name constraints on `.localhost` would need our own CA
  setup instead of Caddy's default one. **Risk: accepted for the spike.**
- Trust cannot be automated in CI (it needs the dialog); the keychain logic
  is unit-tested against a fake `security`.
