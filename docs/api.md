# Local API

The app (and later the MCP server) talk to the core through the Benbow
daemon: JSON over HTTP on a Unix socket, `run/benbow.sock` in the Benbow folder.
There is no TCP port.

```sh
benbow daemon            # status
benbow daemon start      # launchd agent dev.benbow.core; the app does this
benbow daemon stop
```

- The daemon is the launchd user agent `dev.benbow.core`, started by the
  app (not at login). It runs as the user, never as root. A new `benbow`
  binary (its SHA-256 is in the agent) restarts it. `benbow stop` and the
  API's stop never stop the daemon; `benbow uninstall` does.
- launchd restarts the daemon only when it fails (`KeepAlive` with
  `SuccessfulExit` false). A second daemon that finds the socket answering
  exits 0, so it is not started again every few seconds.
- The socket is `0600` in a `0700` folder, and every connection's peer UID
  is checked: only the user who runs the daemon can connect.
- The CLI keeps running the core in-process; both share Benbow's lock.
- A change is never cancelled half way by a client hanging up.

## Envelope and errors

Every answer uses the same envelope as `benbow --json`:
`{"schema":1,"ok":true,"data":…}` or
`{"schema":1,"ok":false,"error":{"code":"…","message":"…"}}`.
Request bodies are strict JSON (unknown fields are refused).

| HTTP | Codes |
|---|---|
| 400 | `invalid_input`, `forbidden_folder` |
| 403 | `forbidden` |
| 404 | `not_found` |
| 409 | `conflict`, `port_in_use`, `confirmation_required`, `locked` |
| 422 | `unsupported` |
| 502 | `download_failed`, `checksum_mismatch` |
| 500 | everything else |

`benbow` exits with a code per error, so scripts can tell failures apart
without `--json` (it never changes, it only grows): 0 ok, 1 anything else
(`internal`, `service_failed`), 2 `invalid_input`/`forbidden_folder` (usage
errors included), 3 `not_found`, 4 `conflict`/`port_in_use`/`locked`, 5
`confirmation_required`, 6 `forbidden`, 7 `unsupported`, 8
`download_failed`/`checksum_mismatch`. `benbow wp`, `composer` and `artisan`
pass on the tool's own exit code.

The data of every answer the app decodes is pinned by
`app/Tests/BenbowKitTests/Fixtures/wire/*.json`: the Go test `TestWireGolden`
(`internal/api`) writes each type with every field filled in, and the Swift
test `decodesWireFixtures` decodes each file into its model. A change shows
up as a diff of those files: adding a field is fine (rerun the Go test with
`-update`), renaming, retyping or removing one breaks the app.

## Routes

| Route | Body / query | CLI |
|---|---|---|
| `GET /v1/version` | | `benbow version` |
| `GET /v1/status` | | `benbow status` |
| `POST /v1/start` | `{https_port}` (optional) | `benbow start` |
| `POST /v1/stop` | | `benbow stop` |
| `GET /v1/sites` | | `benbow site list` |
| `POST /v1/sites` | `{name, domain, php, path, force, create_folder, aliases, group_id, php_settings, new_database, link_database}`; apps: `{command, port, proxy_port, app_host}` (`apps.md`) | `benbow site add` |
| `PATCH /v1/sites/{name}` | any of `{php, path, domain, enabled, archived, aliases, group_id, title, force}` (`group_id` 0: top level); apps: `{command, port, proxy_port, app_host}`; PHP sites: `{companion_command, companion_port}` | `benbow site set`, `enable`, `disable`, `archive`, `unarchive` |
| `GET /v1/detect?path=` | | `benbow site detect` |
| `POST /v1/import/archive` | `{file, name, php, folder, dry_run}` | `benbow import archive` |
| `POST /v1/sites/{name}/clone` | `{name, php, folder, same_folder}` | `benbow site clone` |
| `POST /v1/sites/{name}/restart` | | `benbow site restart` (apps, `apps.md`) |
| `GET`, `PATCH /v1/sites/{name}/env` | `{set: {NAME: value}, unset: [NAME]}`; answers names only | `benbow site env` |
| `POST /v1/laravel` | `{name, path, php, database, domain, aliases, group_id, php_settings}` | `benbow laravel` |
| `POST /v1/sites/{name}/run` | `{tool, args}` (`wp`, `composer`, `artisan`) | `benbow wp`, `benbow composer`, `benbow artisan` |
| `POST /v1/sites/{name}/php-check` | `{target, all}` | `benbow php check` |
| `GET /v1/postgres`, `POST /v1/postgres/{enable,disable}`, `GET /v1/postgres/{credentials,databases}` | `{port}` | `benbow postgres`, `benbow db list --engine pgsql` |
| `GET /v1/redis`, `POST /v1/redis/{enable,disable}` | `{port}` | `benbow redis` |
| `GET`, `POST /v1/sites/{name}/snapshots`, `POST …/snapshots/{id}/restore`, `DELETE …/snapshots/{id}`, `GET`, `PUT /v1/snapshots/keep`, `GET /v1/snapshots/usage?site=`, `POST /v1/snapshots/prune` | `{note}`, restore `{php}`, `{keep}`, prune `{site, older_than_days, dry_run}` | `benbow snapshot` |
| `GET /v1/agent/sessions`, `GET /v1/sites/{name}/agent-session`, `POST …/agent-session/keep`, `…/revert`, `GET …/agent-session/diff`, `GET`, `PUT /v1/agent/idle` | `?all=1`, `?file=`, `{hours}` | `benbow agent session`, `benbow agent diff`, `benbow agent idle` (`agents.md`) |
| `GET /v1/agent/tools`, `POST /v1/agent/tools/{id}/connect`, `POST /v1/sites/{name}/agent/start` | `{tool, terminal, connect}` | `benbow agent tools`, `connect`, `start` |
| `POST /v1/sites/{name}/fetch`, `POST /v1/sites/{name}/query`, `GET /v1/mail/messages`, `GET /v1/mail/messages/{id}` | `{path, method, html}`, `{sql}`, `?to=&subject=&limit=` | `benbow fetch`, `benbow db query`, `benbow mail list`, `show` |
| `GET /v1/sites/{name}/remotes`, `PUT`, `DELETE …/remotes/{remote}`, `POST …/remotes/{remote}/pull\|push` | see `sync.md` | `benbow remote`, `benbow pull`, `benbow push` |
| `GET /v1/mcp/permissions`, `PUT` `{scope, write}`, `GET /v1/mcp/actions` | see `mcp.md` | `benbow mcp allow\|deny\|status\|log` |
| `GET /v1/blueprints`, `GET\|PUT\|DELETE /v1/blueprints/{name}` (PUT body: the JSON, `?overwrite=1`), `POST /v1/blueprints/{name}/apply` `{site, dry_run, accept_changes}`, `POST /v1/sites/{name}/blueprint` `{name, overwrite}`, `GET /v1/blueprints/sources`, `GET\|PUT /v1/github-token` | see `blueprints.md`; `POST /v1/wordpress` takes `blueprint`, `accept_changes` | `benbow blueprint`, `benbow wordpress --blueprint` |
| `GET /v1/phone`, `PUT /v1/sites/{name}/phone`, `DELETE /v1/phone` | `{minutes}` | `benbow phone` |
| `POST /v1/import/{mamp,local,herd,valet,xampp}` | `{dry_run, localhost, sites, migrate, existing, dump_file, source_user, source_password}` | `benbow import <source>` |
| `POST /v1/sites/{name}/migration/undo` | | `benbow import undo` |
| `GET /v1/ports`, `PUT /v1/ports` | `{https_port, http_port}` (0: off) | `benbow ports` |
| `GET`, `POST /v1/groups` | `{name, parent_id}` | `benbow group list`, `add` |
| `PATCH /v1/groups/{id}` | any of `{name, parent_id}` (0: top level) | `benbow group rename`, `move` |
| `DELETE /v1/groups/{id}` | | `benbow group remove` |
| `POST /v1/groups/{id}/enabled` | `{enabled}` | `benbow group enable`, `disable` |
| `PUT /v1/list-order` | `{group, items}` (group 0: top level; items `group:<id>` / `site:<name>`, mixed, in order) | `benbow group order` |
| `DELETE /v1/sites/{name}` | `?drop_database=true` | `benbow site remove [--drop-database]` |
| `GET /v1/sites/{name}/logs/{access\|php\|app\|companion}` | `?lines=N` | `benbow site logs` (`--php`, `--app`, `--companion`) |
| `DELETE /v1/sites/{name}/logs` | | `benbow site logs --clear` |
| `GET`, `PATCH /v1/sites/{name}/php-settings` | `{"memory_limit": "1G"}`, `""` resets | `benbow site php` |
| `GET /v1/sites/{name}/database` | | `benbow db show` |
| `POST /v1/sites/{name}/database` | `{name, link}` | `benbow db create`, `benbow db link` |
| `DELETE /v1/sites/{name}/database` | `?drop=true` | `benbow db unlink` |
| `GET /v1/databases` | | `benbow db list` |
| `POST /v1/wordpress` | `{name, path, php, title, admin_user, admin_email, domain, aliases, group_id, php_settings}` | `benbow wordpress` |
| `GET`, `POST /v1/php` | `{version}` | `benbow php list`, `install` |
| `DELETE /v1/php/{minor}` | | `benbow php remove` |
| `GET`, `PATCH /v1/php/{minor}/settings` | `{memory_limit, upload_max_filesize, max_execution_time, display_errors}` | `benbow php settings` |
| `GET /v1/xdebug`, `PUT /v1/xdebug/{php}` | `{enabled}` | `benbow php xdebug` |
| `GET /v1/domains`, `POST /v1/domains/{zone}/enable\|disable` | | `benbow domains` |
| `GET /v1/mysql`, `POST /v1/mysql/enable\|disable` | `{port}` | `benbow mysql` |
| `GET /v1/mysql/credentials` | | `benbow mysql password` |
| `GET /v1/mysql57`, `POST /v1/mysql57/enable\|disable`, `GET /v1/mysql57/credentials`, `GET /v1/mysql57/databases` | `{port}` | `benbow mysql57` (the optional MySQL 5.7 server) |
| `GET /v1/mail`, `POST /v1/mail/enable\|disable` | | `benbow mail` |
| `GET /v1/updates`, `POST /v1/updates/check\|apply\|rollback`, `PUT /v1/updates/auto` | `{components}`, `{component}`, `{on}` | `benbow update`, `check`, `apply`, `rollback`, `auto on\|off` |
| `POST /v1/adminer/login` | `{site}` (empty: root) | (app only) |
| `POST /v1/adminer/redeem` | `{token}` | (Benbow's Adminer front file) |
| `POST /v1/trust`, `POST /v1/untrust` | | `benbow trust`, `untrust` |
| `POST /v1/import/mamp` | `{dry_run, localhost}` | `benbow import mamp` |

Responses are the same `data` as the matching CLI command with `--json`.
