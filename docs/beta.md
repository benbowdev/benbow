# Benbow beta: notes for testers

Thanks for trying Benbow before everyone else. It is a beta: expect rough
edges, and tell us about them.

## Requirements

- A Mac with Apple Silicon (M1 or later): MySQL has no Intel build
- macOS 14 Sonoma or later (PHP 5.6–7.4 need macOS 15)
- An internet connection the first time: Benbow downloads PHP, Caddy, MySQL
  and the rest on first use, each checked against a pinned SHA-256

## Install

1. Open `Benbow-<version>.dmg` and drag Benbow into Applications.
2. Open Benbow from Applications. It is signed and notarized by Apple.
3. Press Start. The first time, macOS asks for your login password once to
   trust Benbow's local certificate (it can only vouch for `.localhost`,
   `.test` and `.local` addresses, never real websites).

Benbow keeps everything in `~/Library/Application Support/benbow/`. It never
uses Homebrew and never touches MAMP, Valet, Herd or another MySQL: they can
keep running side by side (Benbow uses its own ports: HTTPS 8443, MySQL 3307).

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

- the version: Benbow → About Benbow, or `benbow version` (below)
- the site's Logs window (PHP errors and requests)
- `~/Library/Application Support/benbow/logs/` (Benbow's own logs)

The command line is inside the app:

```sh
/Applications/Benbow.app/Contents/Resources/bin/benbow status
```

## Updating

Quit Benbow, replace it in Applications with the new version, open it: Benbow
restarts its background service with the new version by itself.

## Uninstall

```sh
/Applications/Benbow.app/Contents/Resources/bin/benbow uninstall
```

stops everything, removes Benbow's certificate and keychain items and
deletes `~/Library/Application Support/benbow/` (Benbow's databases included:
export what you need first). Your site folders are never touched. Then
drag Benbow from Applications to the Trash.
