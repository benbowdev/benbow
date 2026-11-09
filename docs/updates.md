# Component updates

This page is about the components (PHP, Caddy, MySQL…). The app itself
updates through Sparkle: see `docs/app.md` and `docs/release.md`.

```sh
benbow update                  # what is available (no network)
benbow update check            # download and verify the signed manifest
benbow update auto on|off      # the daemon's daily check (on by default)
benbow update apply            # install everything available
benbow update apply caddy php-8.4
benbow update rollback mailpit # back to the version before the last update
```

## Trust

Benbow only installs artifacts listed in a manifest: the one compiled into the
binary, plus newer ones published by `benbowdev/benbow-binaries`:

- `manifest/manifest.json` and `manifest.json.sig` (ed25519, base64) on the
  `main` branch of `benbowdev/benbow-binaries`. The signature must match a
  public key compiled into Benbow (`components.TrustedKeys`); the private key
  only exists as a GitHub Actions secret of that repository.
  The end-to-end tests sign with their own key (`BENBOW_MANIFEST_TEST_KEY`),
  which only a binary built with `-tags benbowtest` accepts.
- A published manifest must have a higher `serial` than the one Benbow has,
  so an old manifest can never be replayed. It is merged with the embedded
  one: it can add versions but never change an artifact Benbow already knows
  (same component, version, architecture: different URL or checksum is
  refused), and every artifact URL must be HTTPS.
- A published manifest carries an `expires` date, 14 days after it was
  signed. `benbow-binaries` signs it again when less than 7 days are left,
  even with nothing new. Benbow refuses a manifest without one or past it
  (`checksum_mismatch`, "the published manifest expired on …"): a stuck
  server, or someone serving an old copy, would otherwise hide newer
  versions, security fixes included. The versions in use do not change,
  and the error shows where updates are listed.
- Every download is then checked against the manifest's SHA-256 (and the
  upstream SHA-512 or MD5 where there is one), and binaries that report
  their version (Caddy, PHP) must report the one the manifest claims.
- The accepted manifest is kept in `data/manifest/` and checked again on
  every start.

## Where Benbow looks, and what is counted

The same signed manifest is published in two places by
`benbowdev/benbow-binaries`' daily workflow:

- the release asset
  `https://github.com/benbowdev/benbow-binaries/releases/download/manifest/manifest.json`
  (and `.sig`), replaced once a day;
- `https://raw.githubusercontent.com/benbowdev/benbow-binaries/main/manifest/manifest.json`.

**The automatic check** runs in the daemon (which the app starts): 3
minutes after it starts, then only when the last automatic check that
worked is at least 24 hours old (the daemon looks at the clock every 30
minutes, since timers stop while the Mac sleeps; after a failure it waits
an hour). It asks the release asset first and falls back to `main` if the
asset cannot be downloaded (for a few seconds a day it is missing while the
workflow replaces it), has a bad signature or has expired.

**Manual checks** (Check Now, `benbow update check`) ask `main` only.

**What is counted.** GitHub counts the downloads of each release asset.
Since a Mac checks automatically at most once every 24 hours, the release
asset's daily download count is about the number of Macs that ran Benbow
that day. The workflow writes it, once a day, to the public
`stats/manifest-checks.csv` of `benbowdev/benbow-binaries`
(`period_start,period_end,checks`); `scripts/stats.sh` sums it by day and
adds the app's DMG downloads per version. That total is all we see.

**What is not.** Benbow sends no identifier, cookie or extra parameter:
only the request itself, with the User-Agent `Benbow/<version> (macOS
<version>; <arch>)` that Benbow puts on all its network requests. GitHub
receives the IP address and that User-Agent like for any download; we do
not. Manual checks, Macs that only use the CLI (no daemon) and Benbow
versions before 0.1.0-beta.4 are not counted.

**Turning it off.** Settings → General → Updates → "Check for component
updates daily", or `benbow update auto off`: Benbow then makes no update
request by itself; Check Now still works. (The app's own updates are a
separate setting: Sparkle reads `benbow.dev/appcast.xml`, see
`docs/app.md`.)

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
