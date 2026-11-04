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
  (`covo mcp allow`).

**What it keeps.** A snapshot of the starting state (`snapshots.md`): the
files are cloned at once, the database is dumped by a background process
(agents give an MCP server only seconds to start); changes through Covo
wait until the dump is written. The starting snapshot is never pruned while
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

API: `GET /v1/agent/sessions` (`?all=1`), `GET /v1/sites/{name}/agent-session`,
`POST /v1/sites/{name}/agent-session/keep`, `…/revert`, `GET`, `PUT
/v1/agent/idle` (`hours`).
