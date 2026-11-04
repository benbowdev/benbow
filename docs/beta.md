# Covo beta: notes for testers

Thanks for trying Covo before everyone else. It is a beta: expect rough
edges, and tell us about them.

## Requirements

- A Mac with Apple Silicon (M1 or later): MySQL has no Intel build
- macOS 14 Sonoma or later (PHP 5.6–7.4 need macOS 15)
- An internet connection the first time: Covo downloads PHP, Caddy, MySQL
  and the rest on first use, each checked against a pinned SHA-256

## Install

1. Open `Covo-<version>.dmg` and drag Covo into Applications.
2. Open Covo from Applications. It is signed and notarized by Apple.
3. Press Start. The first time, macOS asks for your login password once to
   trust Covo's local certificate (it can only vouch for `.localhost`,
   `.test` and `.local` addresses, never real websites).

Covo keeps everything in `~/Library/Application Support/covo/`. It never
uses Homebrew and never touches MAMP, Valet, Herd or another MySQL: they can
keep running side by side (Covo uses its own ports: HTTPS 8443, MySQL 3307).

## What to try

- Add a site: drag a project folder into the window, pick PHP, open it
- A new WordPress site (+ → New WordPress Site…), a database per site,
  Adminer (database icon in the toolbar)
- Import from MAMP PRO, Local, Herd, Valet or XAMPP (+ → Import…)
- Snapshots, Open on Phone (QR code), Production (pull over SSH/FTP)
- AI agents: Work with an Agent… on a site, then What Changed / Keep / Undo
  All (Claude Code, Codex, Gemini CLI, Cursor, Copilot CLI if installed)

## Reporting a problem

Tell us what you did, what you expected and what happened, plus:

- the version: Covo → About Covo, or `covo version` (below)
- the site's Logs window (PHP errors and requests)
- `~/Library/Application Support/covo/logs/` (Covo's own logs)

The command line is inside the app:

```sh
/Applications/Covo.app/Contents/Resources/bin/covo status
```

## Updating

Quit Covo, replace it in Applications with the new version, open it: Covo
restarts its background service with the new version by itself.

## Uninstall

```sh
/Applications/Covo.app/Contents/Resources/bin/covo uninstall
```

stops everything, removes Covo's certificate and keychain items and
deletes `~/Library/Application Support/covo/` (Covo's databases included:
export what you need first). Your site folders are never touched. Then
drag Covo from Applications to the Trash.
