# Covo

A native macOS local development environment for people who build PHP,
WordPress and Laravel sites: an open source, modern alternative to MAMP PRO.

Covo never breaks your Mac. It does not use or modify Homebrew, installs
nothing system-wide, and coexists with MAMP, Valet, Herd or anything else
already running. Everything lives in `~/Library/Application Support/covo/`,
and `covo uninstall` puts your Mac back the way it was.

**Status:** public beta. Expect rough edges and
[tell us about them](https://github.com/getcovo/covo/issues).

## Install

Download the latest DMG from
[Releases](https://github.com/getcovo/covo/releases), open it and drag Covo
into Applications. It is signed and notarized by Apple and updates itself.

Requirements: a Mac with Apple Silicon and macOS 14 Sonoma or later. Covo
downloads PHP, Caddy, MySQL and the rest on first use, each checked against
a pinned SHA-256. More in [docs/beta.md](docs/beta.md).

## What it does

- Unlimited sites, each on its own PHP version (5.6 to 8.5), served by Caddy
  over HTTPS at `https://<site>.localhost:8443`, or on 443 if you want
- MySQL 8.4, PostgreSQL 18 and Redis 8, a database per site in one click,
  Adminer already logged in
- WordPress and Laravel in one step, wp-cli, Composer, Mailpit for outgoing
  mail, Xdebug per site
- Import from MAMP PRO, Local, Herd, Valet and XAMPP, database included,
  with undo
- Snapshots of files and database (APFS clones), pull from production over
  SSH, SFTP or FTP, open a site on your phone with a QR code
- A CLI where every command supports `--json`, and an MCP server

### A safe workbench for AI agents

Git protects your code, but nothing protects your database. Covo saves a
site's files **and database** before an agent works on it, shows what
changed, and lets you keep everything or undo it all
([docs/agents.md](docs/agents.md)). It works with Claude Code, Codex, Gemini
CLI, Cursor and Copilot CLI. Agents get read-only access by default. Covo
never runs a model and never asks for an API key.

For Claude Code, the Covo plugin connects Covo and teaches the workflow:

```
/plugin install covo --marketplace getcovo/covo
```

## How it stays out of the way

- Its own local CA, limited by Name Constraints to `.localhost`, `.test` and
  `.local`: it cannot vouch for a real website ([docs/ca.md](docs/ca.md))
- Services run as your user, as launchd agents named `dev.getcovo.*`. They
  listen on loopback only (Open on Phone, when you turn it on, adds your
  local network for one site) and Covo checks a port is free first.
  Nothing runs as root, except an optional helper for `.local` names and
  ports 80/443, installed only after you approve it
  ([docs/domains.md](docs/domains.md), [docs/ports.md](docs/ports.md))
- Component updates come from an ed25519-signed manifest and are installed
  only when you click ([docs/updates.md](docs/updates.md))
- Passwords live in your login keychain, never in plain text

The reasons behind each choice are in [docs/decisions.md](docs/decisions.md).

## Build from source

Requires Go (from go.dev) and Xcode's command line tools.

```sh
go build -o covo ./cmd/covo && go build -o covo-hostshelper ./cmd/covo-hostshelper
./covo site add demo --php 8.4 --path ~/Sites/demo
./covo start
./covo trust          # one macOS confirmation, no sudo
open https://demo.localhost:8443

app/build.sh          # the app, in app/build/Covo.app
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

Covo is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License, version 3 only
(`SPDX-License-Identifier: GPL-3.0-only`). See [LICENSE](LICENSE).

Covo is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE.

The Covo name and logo are not covered by the license: see
[TRADEMARKS.md](TRADEMARKS.md). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md) and the
[Code of Conduct](CODE_OF_CONDUCT.md). Security reports: see
[SECURITY.md](SECURITY.md).
