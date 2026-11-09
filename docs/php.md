# PHP

Benbow runs static PHP-FPM builds, one process per minor version, shared by
every site on that version.

```sh
benbow php list
benbow php install 8.3                 # also happens when a site needs it
benbow php settings 8.4                # show php.ini settings
benbow php settings 8.4 memory_limit=1G upload_max_filesize=256M max_execution_time=0 display_errors=off
```

## Versions

PHP 8.0 to 8.5 are offered as static-php-cli builds. PHP 5.6 to 7.4 (there
was never a PHP 6) come from `benbowdev/benbow-binaries`' `legacy-php` workflow:
shivammathur/php's builds, which carry security fixes backported after these
versions' end of life, made self-contained (every library in `lib/`, shared
extensions such as 7.4's intl in `ext/`, ad-hoc signed, checked with
Homebrew moved away). They reach Benbow through the signed manifest, for Apple
Silicon and macOS 15 or later only (`min_macos`; older macOS gets a clear
error). Benbow loads the `.so` files in `ext/` from its php.ini and starts
PHP-FPM with an empty `PHP_INI_SCAN_DIR`, so a build never reads `.ini`
files from outside Benbow. php-fpm.conf leaves out directives older versions
do not know (`decorate_workers_output` before 7.3). `benbow php list --json` reports php.net's support dates for each
version (`support`: `active`, `security` for security fixes only, `eol`).
Versions past their end of life (8.0 and 8.1 as of November 2026) stay
available for old sites, with a warning in the app and from
`benbow site add`/`set` (`warning` in the JSON): they no longer get security
fixes. Xdebug builds exist only for the versions listed in `xdebug.md`.

## Compatibility check

```sh
benbow php check shop                 # against the newest fully supported PHP
benbow php check shop --target 8.2    # a WordPress site: wp-content only; --all for core too
```

Before switching a legacy site to a newer PHP, Benbow lists what in its code
would break: PHP_CodeSniffer 4.0.4 with PHPCompatibility 10.0.0-alpha2 (and,
for WordPress, PHPCompatibilityWP, which knows WordPress's polyfills). All are
pinned by SHA-256 in the manifest (PHP_CodeSniffer's against GitHub's
published digest) and run with Benbow's PHP 8.4 CLI, without php.ini. The code
is only read. Errors break on the target version; warnings are deprecations.
`node_modules`, `.git`, `wp-content/uploads` and `wp-content/cache` are
skipped. PHPCompatibility 10 is still an alpha, but it is the only release
that knows PHP 8. In the app: the site's PHP Check tool, or the button under the warning of an outdated PHP version.

API: `POST /v1/sites/{name}/php-check` with `target`, `all`.

| Setting | Default | Values |
|---|---|---|
| `memory_limit` | 512M | size (`K`, `M`, `G`) or `-1` (no limit) |
| `upload_max_filesize` | 128M | size; `post_max_size` always follows it |
| `max_execution_time` | 300 | seconds, `0` = no limit, max 86400 |
| `display_errors` | on | on/off (also sets `display_startup_errors`) |

Changes apply to every site on that version: PHP-FPM restarts at once if
Benbow is running. Values are validated before anything is saved, so nothing
else can be written into php.ini. Errors always go to the PHP error log
(`log_errors = On`, `error_reporting = E_ALL`).

## Per site

```sh
benbow site php shop                                  # effective values, and where each comes from
benbow site php shop memory_limit=1G display_errors=off
benbow site php shop memory_limit=                    # back to the PHP version's value
```

A site's own values sit on top of its PHP version's. They reach PHP-FPM as
the FastCGI `PHP_VALUE` of that site's requests, together with its error
log; every setting is always sent, because PHP-FPM keeps a `PHP_VALUE` in the
worker for later requests and a site must never inherit another's. In the
app: site detail → Advanced → PHP settings. API: `GET` and
`PATCH /v1/sites/{name}/php-settings` (`{"memory_limit": "1G"}`, `""` resets).

In the app: Settings → PHP. API: `GET` and `PATCH /v1/php/{minor}/settings`.

Xdebug: see `xdebug.md`.
