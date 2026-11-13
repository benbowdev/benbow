<img src="docs/brand/source/benbow-icon-1024.png" width="96" height="96" alt="The Benbow icon: a pixel-art ship's lantern with a lit flame">

# Benbow

**Local PHP development on your Mac, without breaking it.**

Benbow is a free, open source Mac app that runs WordPress, Laravel, Symfony,
Drupal or plain PHP, each site on its own PHP version from 5.6 to 8.5, all
at once, with trusted HTTPS, MySQL or PostgreSQL, Redis and a mail catcher
on board, plus the Vite or Next.js front end next door. Bring your AI agent too: Benbow saves the site's files and database
first, and you keep or undo whatever it did.

It never touches Homebrew, needs no Docker, installs nothing system-wide and
runs next to MAMP, Valet, Herd or anything else you already have. Each PHP
version is a native binary of about 200 MB, so there are no images to clean
up. Everything lives in
`~/Library/Application Support/benbow/`, and `benbow uninstall` puts your Mac
back the way it was. A modern alternative to MAMP PRO, Local and Herd.

**Website:** [benbow.dev](https://benbow.dev) · **Status:** public beta.
Expect rough edges and [tell us about them](https://github.com/benbowdev/benbow/issues).

## Install

Download the latest DMG from [benbow.dev/download](https://benbow.dev/download)
or [Releases](https://github.com/benbowdev/benbow/releases), open it and drag
Benbow into Applications. It is signed and notarized by Apple and updates
itself.

Requirements: a Mac with Apple Silicon and macOS 14 Sonoma or later. Benbow
downloads PHP, Caddy, MySQL and the rest on first use, each checked against
a pinned SHA-256. More in [docs/beta.md](docs/beta.md).

## A safe workbench for your AI agent

Git guards your code; Benbow guards the code and the database together. Click
**Work with an Agent** on a site and Benbow saves its files and database,
opens your terminal with the agent already connected, and steps back. When
it is done you see which files and tables changed, then keep it all or undo
it all ([docs/agents.md](docs/agents.md)).

- Works with Claude Code, Codex, Gemini CLI, Cursor and Copilot CLI
- Agents check their own work: they open pages and see that request's PHP
  errors, read caught mail and query the database read only
- A checkpoint before risky moves like plugin updates, migrations or a
  search-replace
- Read only by default; deleting, pushing to servers and the final keep or
  undo stay with you
- Benbow is not an agent: it never runs a model and never asks for an API key

For Claude Code, the Benbow plugin connects Benbow and teaches the workflow:

```
/plugin install benbow --marketplace benbowdev/benbow
```

## What it does

- **Any PHP app:** WordPress, Laravel, Symfony, Drupal, Craft CMS,
  Statamic, Kirby, Joomla or your own `index.php`. Drag a folder in and it is
  served at `https://<site>.localhost:8443` (or on 443), with a certificate
  your browsers already trust
- **Every PHP from 5.6 to 8.5**, side by side, one PHP-FPM per version;
  php.ini per version and per site, Xdebug per site, and a check of what
  would break before you move a site to a newer PHP
- **Databases:** MySQL 8.4, PostgreSQL 18 and Redis 8, a database per site
  in one click, Adminer already logged in
- **New sites in one step:** WordPress (with blueprints of themes and
  plugins) and Laravel; wp-cli, Composer and Artisan with the site's PHP
- **Mailpit** catches every email your sites send
- **Moving in:** import from MAMP PRO, Local, Herd, Valet and XAMPP,
  database included, with undo
- **Snapshots** of files and database (APFS clones), **pull from
  production** over SSH, SFTP or FTP, push to staging, and **open a site on
  your phone** with a QR code
- **A CLI** where every command supports `--json`, and an **MCP server**

## The front end next door

Not every project is all PHP. A Next.js, Vite, Astro, Nuxt or SvelteKit
front end, or a Django or FastAPI service, can be a Benbow site too: its own
domain with trusted HTTPS, logs, a database and an on/off switch, like the
PHP sites beside it ([docs/apps.md](docs/apps.md)).

```sh
benbow site add shop --path ~/Code/shop --command "npm run dev -- --port {port} --strictPort"
benbow site add api --path ~/Code/api --proxy-port 3000   # you start the server, Benbow adds the address
```

- Benbow runs your command through your login shell, so nvm, fnm, mise,
  volta or pyenv work as they do in your terminal. It never installs Node
  or Python
- `APP_URL`, `DATABASE_URL`, `REDIS_URL` and the mail settings reach the app
  on their own; your own variables live in the keychain
- Laravel with Vite gets a companion dev server and a certificate for the
  site's domain, so hot reload works over HTTPS
- A dev server that crashes stays down and shows its last lines, instead of
  restarting in a loop

## How it stays out of the way

- Its own local CA, limited by Name Constraints to `.localhost`, `.test` and
  `.local`: it cannot vouch for a real website ([docs/ca.md](docs/ca.md))
- Services run as your user, as launchd agents named `dev.benbow.*`. They
  listen on loopback only (Open on Phone, when you turn it on, adds your
  local network for one site) and Benbow checks a port is free first.
  Nothing runs as root, except an optional helper for `.local` names and
  ports 80/443, installed only after you approve it
  ([docs/domains.md](docs/domains.md), [docs/ports.md](docs/ports.md))
- Component updates come from an ed25519-signed manifest and are installed
  only when you click ([docs/updates.md](docs/updates.md))
- Passwords live in your login keychain, never in plain text

The reasons behind each choice are in [docs/decisions.md](docs/decisions.md).

## Build from source

Requires Go (from go.dev) and Xcode. With only the command line tools the
app still builds, but macOS 26 shows its icon on a grey plate.

```sh
go build -o benbow ./cmd/benbow && go build -o benbow-hostshelper ./cmd/benbow-hostshelper
./benbow site add demo --php 8.4 --path ~/Sites/demo
./benbow start
./benbow trust          # one macOS confirmation, no sudo
open https://demo.localhost:8443

app/build.sh          # the app, as "app/build/Benbow Dev.app"
```

Tests:

```sh
go test ./...
go test -tags integration -timeout 30m ./test/e2e/   # real downloads, launchd and HTTPS, isolated from real installs
go tool golangci-lint run
app/build.sh test
```

## License

Copyright (C) 2026 Andrea Casali.

Benbow is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License, version 3 only
(`SPDX-License-Identifier: GPL-3.0-only`). See [LICENSE](LICENSE).

Benbow is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE.

The Benbow name and logo are not covered by the license: see
[TRADEMARKS.md](TRADEMARKS.md). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Security reports: see
[SECURITY.md](SECURITY.md).
