# MCP server

`benbow mcp` serves the Model Context Protocol over stdin/stdout, so AI agents
can work with Benbow.

Claude Code:

```sh
claude mcp add benbow -- benbow mcp
```

Claude Desktop (`~/Library/Application Support/Claude/claude_desktop_config.json`):

```json
{
  "mcpServers": {
    "benbow": { "command": "/Applications/Benbow.app/Contents/Resources/bin/benbow", "args": ["mcp"] }
  }
}
```

In the app, Settings → AI Agents shows both lines with the full path of the
`benbow` inside the app.

## Read-only by default

Agents can always read. They can change something only where the user
allowed it:

```sh
benbow mcp allow mysite     # changes to one site
benbow mcp allow '*'        # changes everywhere (also add sites, start, stop)
benbow mcp deny mysite
benbow mcp status           # what is allowed
benbow mcp log [-n 50]      # what agents did, refused attempts included
```

The same switches are in Settings → AI Agents, with the recent actions.
A refused change returns `forbidden: AI agents are read-only for …` and is
logged. Tools that do not belong to one site (`start`, `stop`, `add_site`, `clone_site`, `import_archive`,
`new_wordpress`, `new_laravel`, `import_sites`) need `*`.

Every change tool:

- takes `dry_run`: Benbow checks the permission and the arguments and says
  what it would do, without doing it (`import_sites` is a dry run unless the
  agent passes `dry_run: false`);
- works inside the site's agent session (`agents.md`): the session's
  starting snapshot is taken once, and a checkpoint before risky changes
  only (plugin updates, database imports, migrations…); the result names
  them; if saving fails, nothing is changed;
- is written to the action log (last 1000 kept): time, tool, site,
  arguments, result, snapshot. No tool takes a password.

## Tools

| Tool | Changes | What |
|---|---|---|
| `benbow_status` | no | running, services, sites with addresses, databases, mail |
| `list_sites` | no | every site |
| `site_logs` | no | end of a site's PHP error log, request log or app output (kind `app`, the default for apps) |
| `detect_app` | no | the command Benbow proposes for a dev server's folder (`apps.md`) |
| `list_php_versions` | no | installed and available versions, php.net support status |
| `php_compatibility_check` | no | what would break on another PHP version (`php.md`) |
| `list_databases` | no | databases on Benbow's MySQL and PostgreSQL |
| `list_snapshots` | no | a site's snapshots |
| `list_blueprints` | no | saved blueprints, or one's JSON |
| `agent_permissions` | no | what agents may change |
| `session_status` | no | the site's agent session: starting snapshot, checkpoints |
| `fetch_page` | no | a page of a Benbow site with its redirects, text and the PHP errors of that request (`agents.md`) |
| `mail_messages`, `read_mail` | no | the mail PHP sent, caught by Mailpit: list, one message's text and links |
| `query_database` | no | one read-only SQL statement on a site's database (500 rows, 10 s) |
| `session_diff` | no | files and database tables changed since the session started; one file's diff (secrets masked) |
| `app_env` | no | names of an app's variables (Benbow's and the user's), never values |
| `restart_app` | yes | the app site (`apps.md`) |
| `start`, `stop` | yes | all sites and services |
| `import_archive` | yes | a site from a .zip/.tar.gz with its folder and a .sql dump; `dry_run` defaults to true |
| `clone_site` | yes | copy a site under a new name (folder, database, address), or `same_folder` |
| `add_site` | yes | folder, PHP, domain, aliases, new folder, database, php.ini values; an app with `command` or `proxy_port` (`apps.md`) |
| `update_site` | yes | PHP version, folder, domain, aliases, on/off; an app's command, port or proxied port; a PHP site's companion |
| `create_site_database` | yes | database with its own user (password returned once) |
| `new_wordpress`, `new_laravel` | yes | new project in one step (WordPress: optional `blueprint`) |
| `apply_blueprint` | yes | `blueprints.md`; a changed download stops it, and only the user can accept the change |
| `save_blueprint` | yes | a WordPress site as a blueprint (needs `*`) |
| `wp`, `composer`, `artisan` | yes | the tool with the site's PHP; output and exit code |
| `create_snapshot`, `restore_snapshot` | yes | `snapshots.md` (restore snapshots the current state first) |
| `pull` | yes | from a site's remote (`sync.md`); push is not offered to agents |
| `import_sites` | yes | from MAMP PRO, Local, Herd, Valet, XAMPP (`import.md`) |

Like the CLI, the server is a thin client of the core, opened for each call.
On purpose there is no tool that removes a site, a database, a snapshot or
a group, and no push: those stay with the user. `wp` refuses `--prompt` and
`shell`, `artisan` refuses `tinker` (they need a terminal). Output is cut at
100 KiB; a tool run stops after 10 minutes. Errors come back as tool errors
starting with Benbow's error code (`not_found: …`).

Built with the official Go SDK (`github.com/modelcontextprotocol/go-sdk`).
