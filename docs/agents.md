# Working with AI agents

Covo is a safe workbench for the AI agent you already use (Claude Code,
Codex, Gemini CLI, Cursor, GitHub Copilot CLI). Git protects the code;
Covo protects the code **and the database** together. Covo is not an
agent and never calls a model: the agent connects to Covo's MCP server
(`mcp.md`).

## Work with an agent

In the app, a site's detail → **Work with an Agent…** (or `covo agent start
<site> [--tool claude|codex|gemini|cursor-agent|cursor|copilot]
[--terminal Terminal|iTerm|Ghostty] [--connect]`):

1. Covo opens the site's agent session and waits until the starting
   snapshot (files and database) is saved.
2. Agents may change that site **until the session closes** (a temporary
   permission, shown in Settings → AI Agents; separate from `covo mcp
   allow`).
3. Covo starts the agent in the site's folder, in a new window of your
   terminal (Terminal, iTerm or Ghostty; Cursor opens the folder), with
   Covo's MCP server connected and a short context: the site, its address,
   PHP and database, the session and the recommended workflow. The app
   shows the agent on the site until you keep or undo the changes.

Nothing is written into the project folder: the context, a one-run MCP
configuration and the start script live in `tmp/agent/<session>/` in
Covo's folder and go when the session closes.

| Agent | How Covo connects | Context |
|---|---|---|
| Claude Code | `--mcp-config` for that run | `--append-system-prompt`, plus a one-line first prompt |
| Codex (also the one inside the ChatGPT app) | `-c mcp_servers.covo…` for that run | `-c developer_instructions=…` |
| GitHub Copilot CLI | `--additional-mcp-config` for that run | first prompt |
| Gemini CLI | its user settings (`~/.gemini/settings.json`), after you agree and a backup | first prompt |
| Cursor (app and `cursor-agent`) | `~/.cursor/mcp.json`, after you agree and a backup | first prompt; Covo's MCP instructions |

Covo finds agents on your login shell's PATH and in the usual install
folders (`~/.local/bin`, Homebrew, nvm, Volta, Bun, npm) and apps; it never
installs one. The last agent and terminal are remembered.

## Connecting an agent for good

Settings → AI Agents → **Connect…** (or `covo agent connect <agent>`) adds
Covo to an agent's own settings in your home, for use outside Work with an
Agent: Claude Code and Codex with their own `mcp add` command (Claude Code
user scope), Gemini, Cursor and Claude Desktop by adding `mcpServers.covo`
to their JSON file. The file is backed up first in `data/backups/agents/`;
a file that is not valid JSON is left alone. `covo agent tools` lists the
agents and whether Covo is connected.

## Agent sessions

While an agent works on a site, Covo keeps the site's starting state until
you decide:

```sh
covo agent session open shop       # open one yourself before any agent (MCP or not) works
covo agent session list            # open sessions (--all: recent closed ones too)
covo agent session show shop       # when it started, starting snapshot, checkpoints
covo agent session keep shop       # keep the changes
covo agent session revert shop     # undo everything since the start (asks; --yes)
covo agent idle 24                 # idle hours before a session closes by itself (0: never)
```

**When a session opens**

- when an agent starts `covo mcp` inside a site's folder (Claude Code and
  most agents start MCP servers in the project folder; Covo also reads the
  agent's workspace roots). This happens before the agent changes anything,
  so the changes it makes with its own tools are covered too. It also
  happens for sites agents may not change through Covo;
- at the first tool call naming a site the agent may change
  (`covo mcp allow`);
- with `covo agent session open <site>`, for agents that do not use Covo's
  MCP server.

**What it keeps.** A snapshot of the starting state (`snapshots.md`): the
files are cloned at once, the database is dumped by a background process
(agents give an MCP server only seconds to start); changes through Covo
wait until the dump is written (`session open` waits too). A change the
site itself makes in those first seconds may end up in the starting
state. The starting snapshot is never pruned while
the session is open.

**Checkpoints.** Inside a session Covo does not snapshot every call: it takes
a checkpoint only before risky changes:

| Tool | Risky |
|---|---|
| `wp` | `plugin`/`theme` install, update, delete, uninstall; `core` update, download, update-db; `language … install/update/uninstall`; `db` import, reset, query, clean, drop; `search-replace` (not `--dry-run`); `option update/delete` of `siteurl`/`home` |
| `artisan` | `migrate*` except `migrate:status`; `db:wipe`, `db:seed`; `schema:dump --prune` |
| `composer` | `install`, `update`, `upgrade`, `require`, `remove` |
| `update_site` | a new PHP version or folder |

`restore_snapshot`, `pull` and `apply_blueprint` take their own snapshot.
A session keeps its newest 20 checkpoints; they do not count in the
snapshot limit (`covo snapshot keep`).

**Closing.**

- **Keep** closes the session: checkpoints are deleted, the starting
  snapshot stays as an ordinary snapshot (so you can still go back for a
  while; it now counts in the limit).
- **Undo all** (`revert`) puts the files and the database back to the
  start. It also undoes changes **you** made during the session (Covo does
  not know who changed what). `.git` and `node_modules` are never touched,
  and the current state is saved first (a `pre-restore` snapshot: restore
  it to undo the undo).
- A session idle for 24 hours (no tool call on the site, and no agent
  started from Covo still running) closes by itself and keeps the changes
  (Settings → AI Agents, or `covo agent idle`).

In the app: a ✦ mark on the site in the list, a banner in its detail with
**What Changed…**, **Keep Changes** and **Undo All…** (which explains what
it does first), Advanced → AI agent activity, and the menu bar lists the
open sessions.

Agents can see their session (`session_status`) but cannot keep or undo
it: that stays with you.

## What changed

```sh
covo agent diff shop                          # files and tables since the session started
covo agent diff shop --file wp-config.php     # one file's unified diff
```

- **Files**: added, modified and deleted against the starting snapshot
  (`.git` and `node_modules` left out), by size and modification time,
  then contents when only the time changed. At most 500 are listed; the
  counts are always complete.
- **Database tables**: rows inserted, updated and deleted per table, plus
  tables added, dropped or altered. Covo reads the server's own per-table
  counters at the start and now, so it takes milliseconds on any size:
  MySQL's performance schema (Covo turns on only the table I/O counters,
  a few MB; it was off before, and MySQL restarts once to turn it on) and
  PostgreSQL's `pg_stat_user_tables`. If the server restarted during the
  session the counters are gone: changed tables are then found from row
  estimates and update times, marked `approximate`.
- **Secrets**: agents (`session_diff`) see `.env*`, `wp-config.php`,
  `auth.json`, `.npmrc` and key files with passwords, keys, salts and
  tokens masked (`•••`); private keys entirely. The CLI and the app show
  them as they are.

API: `GET /v1/agent/tools`, `POST /v1/agent/tools/{id}/connect`,
`POST /v1/sites/{name}/agent/start` (`tool`, `terminal`, `connect`),
`GET /v1/agent/sessions` (`?all=1`), `GET /v1/sites/{name}/agent-session`,
`GET /v1/sites/{name}/agent-session/diff` (`?file=`),
`POST /v1/sites/{name}/agent-session/keep`, `…/revert`, `GET`, `PUT
/v1/agent/idle` (`hours`).

## Checking the work

Three read-only tools (MCP and CLI) let an agent check what it did:

```sh
covo fetch shop /checkout/            # status, redirects, title, text, PHP errors of that request
covo mail list --to buyer@example.com # the mail PHP sent (Mailpit)
covo mail show                        # the newest message: text and links
covo db query shop "SELECT * FROM wp_options WHERE option_name = 'home'"
```

- **`fetch_page`** requests a page of a Covo site (GET or HEAD; only Covo's
  own addresses, redirects followed only within them) and returns the
  status, main headers, redirects, title, readable text and the PHP errors,
  warnings, notices and deprecations **of that request**. Covo sends an
  `X-Covo-Probe` header; Caddy gives that request its own PHP error log in
  `tmp/probes/`, which Covo reads, appends to the site's log and deletes.
  Other requests are untouched (one header check each).
- **`mail_messages`, `read_mail`** read Mailpit (Settings → Mail): recipient
  and subject filters, the text and the links of a message, attachment
  names.
- **`query_database`** runs one SELECT, SHOW, DESCRIBE, EXPLAIN or WITH
  statement: no `;` inside, no `INTO OUTFILE`, `FOR UPDATE`, file or sleep
  functions. It runs in a read-only transaction (10 seconds, 500 rows) as
  a user that can only read: on MySQL Covo makes one per database
  (`covo_ro_…`, SELECT and SHOW VIEW only, password in the keychain); on
  PostgreSQL the site's own role. Changes go through `wp` or `artisan`
  inside the session.

API: `POST /v1/sites/{name}/fetch` (`path`, `method`, `html`),
`GET /v1/mail/messages` (`to`, `subject`, `limit`), `GET /v1/mail/messages/{id}`
(`latest` for the newest), `POST /v1/sites/{name}/query` (`sql`).

## Teaching agents the workflow

Covo's MCP server describes the recommended workflow to every agent that
connects (`covo_status` and permissions first, work inside the session,
check with `fetch_page`, `site_logs` and mail, show `session_diff` before
finishing).

**Claude Code plugin.** `integrations/claude-code/covo` is a plugin with a
`covo` skill (the workflow) and the MCP server entry for
`/Applications/Covo.app`. The repository is a plugin marketplace
(`.claude-plugin/marketplace.json`):

```
/plugin install covo --marketplace getcovo/covo      # once the repository is public
/plugin marketplace add /path/to/covo                # meanwhile, from a checkout
```

**Other agents (AGENTS.md).** Codex, Cursor and Gemini read an `AGENTS.md`
(Gemini: `GEMINI.md`) in the project. Covo never writes one into a client's
repository; if you want it, paste this yourself:

```markdown
## Local environment: Covo

This site runs in Covo (local PHP/WordPress/Laravel on this Mac). Use Covo's
MCP server ("covo"):
- Start with covo_status and agent_permissions; work inside the site's agent
  session (session_status): Covo saved files and database, the user keeps or
  undoes the changes.
- Run wp, composer and artisan through Covo (site's PHP and database).
- Check with fetch_page (PHP errors of that request), site_logs,
  mail_messages/read_mail, query_database (read-only).
- Before finishing, show session_diff to the user.
```
