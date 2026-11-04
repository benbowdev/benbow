# Working with AI agents

Covo is a safe workbench for the AI agent you already use (Claude Code,
Codex, Gemini CLI, Cursor, GitHub Copilot CLI). Git protects the code;
Covo protects the code **and the database** together. Covo is not an
agent and never calls a model: the agent connects to Covo's MCP server
(`mcp.md`).

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
- A session idle for 24 hours (no tool call on the site) closes by itself
  and keeps the changes.

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

API: `GET /v1/agent/sessions` (`?all=1`), `GET /v1/sites/{name}/agent-session`,
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
