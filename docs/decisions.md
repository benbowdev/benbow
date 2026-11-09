# Decisions

Every architectural and product decision taken since the project started,
with the reason and the alternatives that were turned down. Newest topics
at the end of each section. When a decision changes, the old entry stays
with a line saying what replaced it.

Format: **decision** (date) — why. *Not chosen:* alternatives.

## Product

- **Covo is a simple, modern MAMP PRO alternative for people who build PHP
  and WordPress sites for clients** (2026-10-29) — that audience wants the
  MAMP mental model (one window, sites on the left, detail on the right)
  without its age and lock-in. *Not chosen:* a generic runtime manager like
  FlyEnv (dozens of modules, harder to use).
- **Feature test: if most users will not use it weekly, it goes in
  Advanced or does not ship** (2026-10-29) — simplicity is the product.
  Detail shows only name, folder, PHP, HTTPS, database.
- **macOS only until the public beta** (2026-11-03) — one platform done
  well before spreading out. Windows and Linux maybe after the beta.
- **No Node, Python or other generic runtimes** (2026-11-03) — they belong
  to their own tools; adding them is the road to FlyEnv. Refined on
  2026-11-06 by the next entry.
- **Apps: Covo runs your dev server, it does not manage runtimes**
  (2026-11-06) — a site can be a dev server in any language (Next.js, Vite,
  Django…) behind a Covo domain with trusted HTTPS, started from the user's
  own command or only proxied (`docs/apps.md`). Covo never installs or
  chooses Node, Python or packages: the command runs with the user's own
  version managers. It answers the common case of a PHP site with a
  frontend next to it without becoming a runtime manager. The website,
  README and announcements stay PHP-first. *Not chosen:* managing Node and
  Python versions (FlyEnv's road), Docker, production builds.
- **Apps run through the login shell, are never restarted, stop as a
  whole tree** (2026-11-09) — each start runs `$SHELL -l -i -c` so every
  version manager works as in a terminal without Covo knowing about any of
  them; a crashed dev server stays down with a clear message, because
  restarting a broken command in a loop hides the error and fills the log;
  stopping follows the process group, descendants and an environment
  marker, because dev servers leave workers behind. *Not chosen:* reading
  the PATH once and running the command directly (breaks per-folder
  versions), launchd `KeepAlive`, killing only the main process.
- **Pre-beta block** (2026-11-03): Laravel, PostgreSQL/Redis, Open on
  phone, importers with assisted migration, snapshots, SSH/FTP sync, MCP
  agent safety, blueprints — moved before the beta because they are what
  clients' work needs every week (Laravel, staging/production sync) and
  what makes Covo safer than MAMP (snapshots, agent safety).
- **Covo is the safe workbench for AI agents, not an agent** (2026-11-04)
  — the user brings their agent (Claude Code, Codex, Gemini CLI, Cursor,
  Copilot CLI) and account; Covo protects files and database together,
  which git alone does not. No chat, no API keys, no model calls in Covo.
  See "AI agents" below.

## Golden rules

- **Never run or modify Homebrew** (2026-10-29) — many users' Homebrew is
  fragile and shared with other work; a dev tool that breaks it breaks the
  Mac. Covo only reads Homebrew's MySQL when importing from Valet.
- **Nothing outside `~/Library/Application Support/covo/` without an
  explicit, user-approved step** (2026-10-29) — uninstall must leave the
  Mac exactly as before (verified by an inventory before and after the e2e
  suite and on the real Mac). The listed exceptions: CA in the login
  keychain, `/etc/resolver/test`, Covo's marked block in `/etc/hosts`, the
  helper's LaunchDaemon, pf rules in Covo's own anchor.
- **Never touch services Covo did not start** (MAMP, Valet, Herd,
  Postgres.app, other Redis/MySQL) (2026-10-29) — Covo must coexist; users
  migrate gradually. Importers read other tools' data only.
- **Check every port before binding** (2026-10-29) — MAMP holds 80, 3306,
  8888 on typical Macs; default ports were chosen around them (8443, 3307,
  5433, 6380).
- **Every downloaded binary is verified against a pinned SHA-256**
  (2026-10-29) — Covo runs third-party binaries as the user; nothing is
  extracted or executed before the hash matches.
- **Secrets only in the login keychain** (2026-10-29) — never in files,
  command lines, URLs or the clipboard (MySQL root, site DB users,
  PostgreSQL superuser, remote passwords, GitHub token).

## Identity and licence

- **Name Covo, domain getcovo.dev, org `getcovo`, bundle `dev.getcovo.*`**
  (2026-10-29).
- **GPL-3.0-only, Copyright Andrea Casali** (2026-10-29) — keeps forks open;
  trademark rules in TRADEMARKS.md protect the name.
- **`getcovo/covo` private until the beta; binaries Covo builds published
  from the public `getcovo/covo-binaries`** (2026-10-29) — Covo must
  download them without a token. Releases are never replaced: a rebuild
  gets a new tag. *Not chosen:* making the main repo public early.

## Architecture

- **Go core, SwiftUI app** (2026-10-29) — Go gives a single static binary
  for core, CLI, MCP and helper; SwiftUI gives the native look the product
  needs. *Not chosen:* Electron/Tauri (not native), all-Swift (CLI and
  server code is easier in Go).
- **The core is the only source of truth; app, CLI and MCP are thin
  clients** (2026-10-29) — one behaviour everywhere, tested once, no logic
  in the GUI.
- **Local API: JSON over a Unix socket (0600 + peer UID check), daemon
  `dev.getcovo.core` started by the app** (2026-11-02) — no TCP port to
  protect, only the user can talk to it. The CLI stays in-process (works
  without the daemon); the spike had no daemon at all.
- **The core never runs as root** (2026-10-29) — privileged work (hosts
  file, pf) goes through a minimal helper with a fixed set of operations.
- **launchd user agents `dev.getcovo.*`, plists in the Covo home,
  bootstrapped by Covo, not at login** (2026-10-29) — nothing runs unless
  the user starts Covo; opt-in "open at login" through SMAppService.
- **SQLite state with a pure-Go driver (modernc), numbered migrations**
  (2026-10-29) — no CGO, one file, easy to back up.
- **An older covo refuses a database migrated by a newer one** (2026-11-04)
  — an older copy on PATH, or going back to an older app, would otherwise
  write rows without knowing what the newer columns mean; it answers
  `unsupported` and says to update. A test migrates a schema-0007 database
  with data to the current schema.
- **Every CLI command has `--json` with a stable envelope and error codes**
  (2026-10-29) — scripts and agents can rely on it.
- **Concurrent writers serialized by `flock` on `covo.lock`** (2026-10-29).
- **Exported core methods that change things take the lock; unexported
  ones assume it is held** (2026-11-04) — the flock is not reentrant (a new
  file descriptor waits for the one already held), so a test reads the
  package's source and fails when code under the lock calls a method that
  takes it again. Restore, pull, push, blueprint apply and migration undo
  now hold it for their whole run; other commands wait up to 30 s and then
  answer `locked`. *Not chosen:* a reentrant lock (hides which code runs
  under it).
- **App built with SwiftPM and `app/build.sh`, no Xcode project**
  (2026-11-02) — reproducible from the command line.
- **User secrets go to the keychain hex-encoded** (2026-11-04) — remote
  passwords and the GitHub token are the user's, so any character must
  work; `security -i` takes them as `-X <hex>` on stdin and `-g` reads
  them back (`internal/keychain`). Passwords Covo generates keep their
  quote-free alphabet, since they also go into option files and SQL.
  *Not chosen:* quoting for `security -i` (its escaping rules are not
  documented), the Security framework through cgo (Covo builds without
  cgo).

## Web server and PHP

- **Caddy as the web server** (2026-10-29) — single static binary, local
  HTTPS built in, config reloads without dropping requests. *Not chosen:*
  Apache (heavy, `.htaccess` parity is the only gain — maybe v2), Nginx.
- **Static PHP-FPM builds from static-php-cli, one FPM per version, many
  versions in parallel** (2026-10-29) — self-contained binaries, no
  Homebrew, every extension WordPress needs built in. Trust on first use at
  pinning time because upstream publishes no checksums. *Not chosen:*
  building PHP ourselves (v2 pipeline).
- **Per-site php.ini through FastCGI `PHP_VALUE`, every setting sent on
  every request** (2026-11-02) — PHP-FPM keeps `PHP_VALUE` in the worker,
  so a value set by one site would leak into the next. *Not chosen:* one
  FPM pool per site (more processes, more memory).
- **PHP 7.4 MAMP sites mapped to 8.1 with a warning** (2026-10-29), later
  **legacy PHP 5.6–7.4 offered from shivammathur builds bundled in
  covo-binaries** (2026-11-03) — real client sites still run old PHP;
  the support status and EOL warnings come from php.net.
- **Xdebug built in CI per exact PHP version** (2026-10-29) — static builds
  do not ship it; the toggle is per site in Advanced.
- **wp-cli, Composer, PHP_CodeSniffer run with the site's PHP CLI build and
  an empty `PHP_INI_SCAN_DIR`** (2026-11-02/03) — static builds look in a
  fixed `/usr/local/etc` folder that may hold someone else's config.
- **wp-cli launcher fix via a prepended `launch.php`** (2026-11-03) —
  wp-cli's child processes broke on paths with spaces ("Application
  Support"); fixed without patching wp-cli.

## Domains and the CA

- **`<site>.localhost` on a high HTTPS port (8443) by default**
  (2026-10-29) — resolves without any setup or privilege on macOS.
- **`.test` opt-in: `/etc/resolver/test` + Covo's DNS on 127.0.0.1:53535**
  (2026-10-29) — one admin dialog, reserved TLD.
- **`.local` only as "MAMP compatibility mode" through `/etc/hosts`**
  (2026-10-29) — `.local` is mDNS; it works only via hosts and costs ~1 ms
  per lookup, so it is never the default. Always both 127.0.0.1 and ::1.
- **No non-reserved TLDs (`.covo`) and no public domains** (2026-10-29) —
  they can collide with real names.
- **Own CA with Name Constraints (`.localhost`, `.test`, `.local` only, no
  IPs), constraints on root and intermediate, root key discarded after
  signing** (2026-10-29) — trusting a CA is powerful; Covo's can only vouch
  for local names, and nobody can mint another intermediate. Never weakened
  without the user's explicit consent. *Not chosen:* Caddy's own
  unconstrained CA (used in the spike, migrated away).
- **Provisional privileged helper as a LaunchDaemon (one admin
  authorization)** (2026-10-29) — SMAppService needs a Developer ID-signed
  app, which comes with the beta; the helper only edits Covo's marked
  block in `/etc/hosts` and Covo's pf anchor.
- **Ports 443/80 through pf `rdr` rules in Covo's own anchor, only while
  Covo runs** (2026-11-03) — Caddy stays unprivileged; `pf.conf` is never
  edited; refused if another program has the port.

## Databases

- **MySQL 8.4 LTS from Oracle's archives on port 3307, Apple Silicon only**
  (2026-10-29) — LTS for stability; 3306 is usually MAMP's; Oracle has no
  Intel build after 8.4.11. *Not chosen:* MariaDB by default.
- **Site databases get their own user and keychain password; linked
  existing databases connect as root** (2026-11-02) — least privilege for
  new work, MAMP habit for imported sites.
- **Adminer behind `adminer.localhost`, opened already logged in through
  one-time 60 s tokens** (2026-11-02) — no password in URL, clipboard or
  file.
- **PostgreSQL 18 and Redis 8 built by Covo in covo-binaries, off by
  default, ports 5433/6380** (2026-11-03) — no official self-contained
  macOS builds; 5432/6379 are often taken. Redis 8 (AGPLv3 option) over
  Valkey by the user's choice.
- **Static PHP's PDO pgsql (Swoole's) used over the Unix socket**
  (2026-11-03) — it hangs over TCP; own PHP builds later.

## Updates

- **Signed manifest (ed25519) from covo-binaries, merged with the embedded
  one; higher serial only; entries only added, never changed** (2026-11-02)
  — one trust root for every component; a stolen server cannot swap a
  known artifact or replay an old manifest. Private key only as a GitHub
  Actions secret.
- **The published manifest expires after 14 days** (2026-11-05) — the
  serial stops replays of older manifests, but not a server that keeps
  serving the last one (a freeze): Covo would never hear of security
  fixes. Renewed weekly by the workflow. *Not chosen:* a short expiry (a
  day: one failed run would raise errors everywhere), no expiry.
- **Active users from the release download count, not telemetry**
  (2026-11-05) — the automatic update check downloads the signed manifest
  from a GitHub release asset at most once every 24 hours per Mac; the
  daily download count (public CSV in covo-binaries) is the number of
  active Macs. No identifier, no new service, nothing beyond the request
  GitHub already serves. Manual checks use the raw copy so they do not
  count; the raw copy is also the fallback. *Not chosen:* our own
  telemetry endpoint (a server, personal data, a privacy policy to
  defend), a counter on getcovo.dev or Plausible (the app never visits
  the site), the CSV in a private repository (a token in a public
  workflow; the live count is public through the API anyway).
- **Updates are a badge and a click, never automatic; versions pinned per
  installation** (2026-11-02) — a client site must not change under the
  user's feet; rollback keeps the previous version.

## App

- **One window, sidebar + detail, global Start/Stop, menu bar extra, badge
  for updates** (2026-10-29) — the MAMP PRO mental model, cleaner.
- **Groups are list-only folders, nested to 16 levels** (2026-11-03) — they
  never change how a site is served. A group switch turns its sites on or
  off (2026-11-04). The user orders each level by drag and drop
  (2026-11-04): a `position` per site and group, 0 = by name, so lists
  never ordered keep sorting alphabetically and new items go last only
  once a level was ordered. Groups and sites share the positions of a
  level and mix freely (the user's request). The app uses the
  list's own `.draggable` + `.onInsert` + `.dropDestination`: a custom
  `onDrag`/`DropDelegate` per row is ignored by macOS lists. *Not chosen:*
  `onMove` (no moves between levels).
- **"+" at the bottom of the sidebar, database and settings on the right
  of the toolbar** (2026-11-03) — like Tower; the user's preference.
- **Non-obvious operations are explained inline before the user acts
  (`ExplainBox`)** (2026-11-03) — migration, sync, restore and undo have
  side effects that a confirmation alert alone does not convey.
- **Site tools as tiles, Advanced holds settings only** (2026-11-04) —
  Advanced mixed settings (domain, php.ini, Xdebug) with features people
  look for (snapshots, phone, production sync, commands), which got lost.
  Tiles keep the detail short. *Not chosen:* two collapsed sections (tools
  stay hidden), a Tools menu in the header (no room for descriptions).

## Import and migration

- **Importers read other tools' data only, from temporary copies**
  (2026-10-29) — MAMP and the others keep working untouched.
- **Assisted migration: database copied (source only read), wp-config.php
  or .env backed up in the Covo home then changed (only the four DB
  defines), URLs replaced with `wp search-replace`, undo per site**
  (2026-11-03) — moving a real site by hand was the slow and risky part.
  The project folder stays shared with the old tool: that is explained
  before migrating.
- **Migration also points `WP_HOME`/`WP_SITEURL` at Covo when
  wp-config.php sets them as plain strings** (2026-11-05) — they win over
  the database, so after a migration the site kept redirecting to the old
  `.local` address. `WP_SITEURL` keeps its path under the old home. Defines
  built by code, repeated, or pointing elsewhere are left alone with a
  warning. *Not chosen:* warning only (the fix is mechanical and the file
  is already backed up and being edited).
- **Old and new address replaced through a placeholder** (2026-11-04) — a
  MAMP address without a suffix (`http://site`) is the start of the new
  one, and replacing http and https variants in turn applied it twice.

## Snapshots and sync

- **Snapshot = whole project folder via APFS clonefile minus `.git` and
  `node_modules`, plus a database dump; restore snapshots first**
  (2026-11-03) — clones cost no space until files change; a restore never
  rewinds git or installed packages; every restore can be undone.
- **Keep 10 per site by default** (2026-11-03).
- **Production sync over the system's ssh/rsync; push only to staging;
  snapshot before pull** (2026-11-03) — never install tools; never let a
  click overwrite production.
- **Hosts without SSH: FTPS/FTP or SFTP for files, a temporary PHP bridge
  for the database (random name, 256-bit token, 10 min expiry, always
  removed)** (2026-11-03) — shared hosting is common among the users.
- **SSH passwords in the keychain, given to ssh through `covo askpass`**
  (2026-11-03).

## Phone

- **Open on phone: one site, private IPv4 only, `<site>-<mac>.local` via
  dns-sd, auto-off, CA install page** (2026-11-03) — the CA's constraints
  already cover `.local`, so the CA does not change. URLs fixed through a
  Covo-owned `auto_prepend_file` only for the phone's requests: no database
  or project change.
- **The phone opens a plain-HTTP address first** (2026-11-04) — installing
  a profile and turning on full trust on iPhone takes six taps in Settings
  that Apple does not let anyone shorten, and the user wanted to scan and
  see the site. The traffic stays on the private network for the chosen
  minutes; HTTPS with the certificate remains one click away for what needs
  a secure context. *Not chosen:* weakening or skipping the CA (never
  without asking), a public tunnel (sends the site outside the network).

## Laravel and blueprints

- **Laravel detected by `artisan` + `laravel/framework`, docroot
  `public/`; new projects use the site database by default; Composer
  pinned (2.x and 2.2 LTS)** (2026-11-03).
- **Blueprints: strict JSON, wordpress.org slugs pinned to a version,
  GitHub refs resolved to a commit, SHA-256 recorded per URL on first use
  and refused on change unless accepted; apply snapshots first**
  (2026-11-03) — blueprints install third-party code; trust on first use
  makes later tampering visible. System git only if the Command Line Tools
  exist (never installed).

## AI agents

- **MCP server over stdio with the official Go SDK; no delete tools, no
  push** (2026-11-03) — destructive and outward actions stay with the user.
- **Agents are read-only by default; writes need `covo mcp allow <site|*>`;
  dry_run on every change; every call logged** (2026-11-03).
- **Agent sessions replace "a snapshot per call"** (2026-11-04) — with keep
  10 an agent making more than ten changes lost the starting state, full
  dumps per call were slow on big databases, and changes the agent made
  with its own tools had no checkpoint. Decisions:
  - a session opens when the agent connects from inside a site's folder
    (also for read-only sites) and at the first tool call naming a site it
    may write; the starting snapshot is never pruned while the session is
    open;
  - inside a session, checkpoints only before risky operations, at most 20
    per session, outside the keep limit;
  - the user closes it with Keep or Undo All (which also undoes their own
    edits during the session, never `.git`); after 24 h idle it closes
    keeping the changes;
  - the database dump runs in a detached process, because agents give an
    MCP server only 10–30 s to start.
- **"What changed": files by size/mtime then hash; database by
  performance_schema table I/O counters (minimal instrumentation) and
  `pg_stat_user_tables`** (2026-11-04) — instant on any database size.
  *Not chosen:* information_schema estimates only.
- **Secrets masked in what agents see; names only by default**
  (2026-11-04).
- **`query_database` through a dedicated read-only user per site, in a
  read-only transaction with time and row limits** (2026-11-04) — linked
  databases use root, and validation alone is not a guarantee.
- **`fetch_page` correlates PHP errors to one request through a probe
  header that sends that request's error log to its own file** (2026-11-04)
  — zero cost when unused; only Covo sites, no external URLs.
- **"Work with an agent": MCP config passed per run where the agent allows
  it, otherwise a global entry with confirmation and backup; never files in
  client repositories; context through a system-prompt file outside the
  repo or a first prompt** (2026-11-04).
- **Supported agents: Claude Code, Codex, Gemini CLI, Cursor (app and CLI),
  GitHub Copilot CLI** (2026-11-04).
- **Claude Code plugin in the repo + an AGENTS.md snippet for the others**
  (2026-11-04) — the snippet is for the user to paste: Covo never writes
  into client repositories.
- **Folder check on the agent's first request, not on `initialized`**
  (2026-11-04) — the 2026-07-28 MCP protocol replaced the initialize
  handshake with `server/discover`; roots are deprecated, so the server's
  working folder is the fallback.
- **Session database dump in a detached process; explicit opens wait for
  it** (2026-11-04) — agents give MCP servers 10–30 s to start; the CLI and
  Work with an Agent are not in a hurry, and waiting avoids a change
  slipping into the starting state.
- **Performance schema sized to ~30 MB** (2026-11-04) — the defaults cost
  ~400 MB; zero thread instances lose the table counters, so threads stay
  (300) and every other table is sized to zero.
- **fetch_page sends only GET/HEAD and follows redirects only to Covo
  hosts** (2026-11-04) — checking must not change the site or reach the
  internet.

## Releases

- **Preliminary betas: Developer ID, notarized DMG sent by hand**
  (2026-11-04) — a collaborator tries Covo before the public beta; a
  notarized app opens with a double click and keeps one identity across
  builds (keychain items, login item). Versions `0.1.0-beta.N`; Sparkle and
  a public download come with the public beta. *Not chosen:* ad-hoc
  signing ("Open Anyway" in Settings, keychain prompts after every build),
  a GitHub release (the repository stays private).

- **App updates: Sparkle 2, feed on getcovo.dev, DMG on GitHub releases**
  (2026-11-05) — the standard for apps outside the App Store; EdDSA
  signature on top of Developer ID and notarization. Feed on the domain
  so moving off GitHub later needs no app update. Scheduled checks only
  show a toolbar badge (gentle reminders): Sparkle's window opens only on
  a click, like component updates. XPC services removed (Covo is not
  sandboxed). Development builds have no feed. *Not chosen:* the feed as
  a GitHub release asset (ties every installed copy to GitHub), a
  self-written updater.
- **Public history rewritten before going public** (2026-11-05) — client
  site names and notes about the author's Mac replaced in all commits;
  the original history stays in the private `getcovo/covo-archive`, the
  rewritten one is a new `getcovo/covo` (a force push would leave old
  commits reachable by hash).

## Later (v2+), decided not now

- Own binary build pipeline (signed, reproducible, SBOM); Apache and Nginx;
  SMAppService helper once the app is signed; CVE warnings per site;
  Windows and Linux.
