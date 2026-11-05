# Component updates

This page is about the components (PHP, Caddy, MySQL…). The app itself
updates through Sparkle: see `docs/app.md` and `docs/release.md`.

```sh
covo update                  # what is available (no network)
covo update check            # download and verify the signed manifest
covo update apply            # install everything available
covo update apply caddy php-8.4
covo update rollback mailpit # back to the version before the last update
```

## Trust

Covo only installs artifacts listed in a manifest: the one compiled into the
binary, plus newer ones published by `getcovo/covo-binaries`:

- `manifest/manifest.json` and `manifest.json.sig` (ed25519, base64) on the
  `main` branch of `getcovo/covo-binaries`. The signature must match a
  public key compiled into Covo (`components.TrustedKeys`); the private key
  only exists as a GitHub Actions secret of that repository.
  The end-to-end tests sign with their own key (`COVO_MANIFEST_TEST_KEY`),
  which only a binary built with `-tags covotest` accepts.
- A published manifest must have a higher `serial` than the one Covo has,
  so an old manifest can never be replayed. It is merged with the embedded
  one: it can add versions but never change an artifact Covo already knows
  (same component, version, architecture: different URL or checksum is
  refused), and every artifact URL must be HTTPS.
- Every download is then checked against the manifest's SHA-256 (and the
  upstream SHA-512 or MD5 where there is one), and binaries that report
  their version (Caddy, PHP) must report the one the manifest claims.
- The accepted manifest is kept in `data/manifest/` and checked again on
  every start.

## What updates

| Component | How |
|---|---|
| Caddy, MySQL (8.4 LTS only), Mailpit, Adminer, wp-cli | fixed per installation; an update switches the version and restarts what uses it; the previous version stays for `rollback` |
| PHP | per minor version: the new patch is installed next to the old one and the sites move to it; `rollback php-8.4` goes back |
| Xdebug | follows PHP; a PHP update waits while Xdebug is on and has no build for the new version yet |
| CA bundle, WordPress for new sites | always the newest in the manifest |

Updates stay within a series: Caddy 2.x, Mailpit 1.x, MySQL 8.4.x, PHP
within its minor version. A failed update changes nothing.

The daemon checks once a day (a few minutes after it starts); it never
installs anything by itself. The app shows a badge when updates are
available. API: `GET /v1/updates`, `POST /v1/updates/check`,
`POST /v1/updates/apply` (`{"components": [...]}`, empty = all),
`POST /v1/updates/rollback` (`{"component": "caddy"}`).
