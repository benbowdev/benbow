# The app

`app/` is a SwiftPM package (no Xcode project): `CovoKit` (models and the
API client, tested) and `Covo` (the SwiftUI app, macOS 14+).

```sh
app/build.sh          # builds app/build/Covo.app (ad-hoc signed)
app/build.sh test     # Swift tests, including one against a live daemon
open app/build/Covo.app
```

`Covo.app` carries `covo` and `covo-hostshelper` in
`Contents/Resources/bin`. On launch it runs `covo daemon start` and talks to
the daemon over its socket (`docs/api.md`), refreshing every 2 seconds.
The app has no logic of its own: every change goes through the core.

## Window

- Toolbar: one Start/Stop for all sites with its status; + (New Site,
  New WordPress Site, New Group, Import from MAMP PRO); Mail (when on);
  Adminer (every database, already logged in); Settings.
- New Site sheet: start from an existing folder (Laravel and WordPress are
  recognised), a new empty folder, WordPress or Laravel; name, domain, aliases, PHP version, group, database (none, new,
  existing) and, collapsed, the site's PHP settings. Created in one step.
- Sidebar: groups nested to any depth (drag a row between two others to
  order, macOS draws the line; onto a group to move it inside; or Move To in
  the context menu; collapsed groups are remembered), sites
  with search (results show their group), a switch to turn each site on or
  off, and a context menu (Open in Browser, Show in Finder, Logs, Open in
  Terminal or an installed editor, Edit Title, Duplicate, Move To). A site
  with a title shows it instead of its name (pencil next to the title in
  the detail); the name and address stay. Drop a folder to add
  it; a banner offers to trust the local certificate when needed.
- Detail: name, folder, PHP version, HTTPS, database; a Tools row of tiles
  (Snapshots, Open on Phone, Production, wp-cli/Artisan/Composer, PHP Check,
  Blueprints for WordPress; description on hover); a collapsed Advanced
  section with settings only (domain, aliases, PHP settings of the site,
  Xdebug, migration, archive, remove).
- Logs window per site: PHP errors and requests, refreshed every 2 seconds.

## Settings (⌘, or the gear in the toolbar)

PostgreSQL and Redis tabs: off by default, port, connection details,
Adminer for PostgreSQL (`postgres-redis.md`). General → Ports: HTTPS on 443 (no port in addresses) or a custom port,
HTTP off, 80 or a custom port (redirects to HTTPS); see `ports.md`.

General (HTTPS port, open at login, start sites on launch, certificate),
PHP (versions, php.ini per version), MySQL, Mail, Domains (`.test`,
`.local`).

## Menu bar

The C from the brand kit (`docs/brand`), as a template image so macOS tints
it; dimmed while stopped. It lists the sites for quick opening, Start/Stop,
Adminer, the mail inbox, and Quit. Closing the window keeps Covo in the menu
bar.

## Updates

When the daemon's daily check (or Check Now) finds component updates, a
discreet "n Updates" button appears in the toolbar (on its own, left of
Adminer and Mail; Settings sits apart at the end), with the list and
Update / Update All; the same list is in Settings → General, and the menu
bar offers to install them. Nothing is installed without a click
(`docs/updates.md`).

## Updates of the app itself

Released builds check `https://getcovo.dev/appcast.xml` once a day with
Sparkle. A newer Covo joins the same "n Updates" button as the components,
at the top of its list; Install… there (or Covo → Check for Updates…)
opens Sparkle's window with the release notes and Install. Nothing installs without a click.
Settings → General → Updates turns the daily check off. Development builds
(`app/build.sh` without `COVO_VERSION`) have no feed and never check.

## AI agents

A site's detail has **Work with an Agent…**: pick one of the agents on this
Mac (Claude Code, Codex, Gemini CLI, Cursor, Copilot CLI); Covo saves the
site, lets the agent change it until you decide, and starts the agent in
the site's folder. While an agent session is open the site shows ✦ in the
list and a banner in its detail: What Changed…, Keep Changes, Undo All…;
both turn orange once the agent has finished, until you decide.
The menu bar lists open sessions. Settings → AI Agents: connect agents,
what they may change, when idle sessions close, what they did
(`agents.md`).

