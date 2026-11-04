# Local API

The app (and later the MCP server) talk to the core through the Covo
daemon: JSON over HTTP on a Unix socket, `run/covo.sock` in the Covo folder.
There is no TCP port.

```sh
covo daemon            # status
covo daemon start      # launchd agent dev.getcovo.core; the app does this
covo daemon stop
```

- The daemon is the launchd user agent `dev.getcovo.core`, started by the
  app (not at login). It runs as the user, never as root. A new `covo`
  binary (its SHA-256 is in the agent) restarts it. `covo stop` and the
  API's stop never stop the daemon; `covo uninstall` does.
- The socket is `0600` in a `0700` folder, and every connection's peer UID
  is checked: only the user who runs the daemon can connect.
- The CLI keeps running the core in-process; both share Covo's lock.
- A change is never cancelled half way by a client hanging up.

## Envelope and errors

Every answer uses the same envelope as `covo --json`:
`{"schema":1,"ok":true,"data":…}` or
`{"schema":1,"ok":false,"error":{"code":"…","message":"…"}}`.
Request bodies are strict JSON (unknown fields are refused).

| HTTP | Codes |
|---|---|
| 400 | `invalid_input`, `forbidden_folder` |
| 404 | `not_found` |
| 409 | `conflict`, `port_in_use`, `confirmation_required`, `locked` |
| 422 | `unsupported` |
| 502 | `download_failed`, `checksum_mismatch` |
| 500 | everything else |

## Routes

| Route | Body / query | CLI |
|---|---|---|
| `GET /v1/version` | | `covo version` |
| `GET /v1/status` | | `covo status` |
| `POST /v1/start` | `{https_port}` (optional) | `covo start` |
| `POST /v1/stop` | | `covo stop` |
| `GET /v1/sites` | | `covo site list` |
| `POST /v1/sites` | `{name, domain, php, path, force, create_folder, aliases, group_id, php_settings, new_database, link_database}` | `covo site add` |
| `PATCH /v1/sites/{name}` | any of `{php, path, domain, enabled, archived, aliases, group_id, force}` (`group_id` 0: top level) | `covo site set`, `enable`, `disable`, `archive`, `unarchive` |
| `POST /v1/laravel` | `{name, path, php, database, domain, aliases, group_id, php_settings}` | `covo laravel` |
| `POST /v1/sites/{name}/run` | `{tool, args}` (`wp`, `composer`, `artisan`) | `covo wp`, `covo composer`, `covo artisan` |
| `POST /v1/sites/{name}/php-check` | `{target, all}` | `covo php check` |
| `GET /v1/postgres`, `POST /v1/postgres/{enable,disable}`, `GET /v1/postgres/{credentials,databases}` | `{port}` | `covo postgres`, `covo db list --engine pgsql` |
| `GET /v1/redis`, `POST /v1/redis/{enable,disable}` | `{port}` | `covo redis` |
| `GET`, `POST /v1/sites/{name}/snapshots`, `POST …/snapshots/{id}/restore`, `DELETE …/snapshots/{id}`, `GET`, `PUT /v1/snapshots/keep` | `{note}`, `{keep}` | `covo snapshot` |
| `GET /v1/agent/sessions`, `GET /v1/sites/{name}/agent-session`, `POST …/agent-session/keep`, `…/revert`, `GET …/agent-session/diff`, `GET`, `PUT /v1/agent/idle` | `?all=1`, `?file=`, `{hours}` | `covo agent session`, `covo agent diff`, `covo agent idle` (`agents.md`) |
| `POST /v1/sites/{name}/fetch`, `POST /v1/sites/{name}/query`, `GET /v1/mail/messages`, `GET /v1/mail/messages/{id}` | `{path, method, html}`, `{sql}`, `?to=&subject=&limit=` | `covo fetch`, `covo db query`, `covo mail list`, `show` |
| `GET /v1/sites/{name}/remotes`, `PUT`, `DELETE …/remotes/{remote}`, `POST …/remotes/{remote}/pull\|push` | see `sync.md` | `covo remote`, `covo pull`, `covo push` |
| `GET /v1/mcp/permissions`, `PUT` `{scope, write}`, `GET /v1/mcp/actions` | see `mcp.md` | `covo mcp allow\|deny\|status\|log` |
| `GET /v1/blueprints`, `GET\|PUT\|DELETE /v1/blueprints/{name}` (PUT body: the JSON, `?overwrite=1`), `POST /v1/blueprints/{name}/apply` `{site, dry_run, accept_changes}`, `POST /v1/sites/{name}/blueprint` `{name, overwrite}`, `GET /v1/blueprints/sources`, `GET\|PUT /v1/github-token` | see `blueprints.md`; `POST /v1/wordpress` takes `blueprint`, `accept_changes` | `covo blueprint`, `covo wordpress --blueprint` |
| `GET /v1/phone`, `PUT /v1/sites/{name}/phone`, `DELETE /v1/phone` | `{minutes}` | `covo phone` |
| `POST /v1/import/{mamp,local,herd,valet,xampp}` | `{dry_run, localhost, sites, migrate, existing, dump_file, source_user, source_password}` | `covo import <source>` |
| `POST /v1/sites/{name}/migration/undo` | | `covo import undo` |
| `GET /v1/ports`, `PUT /v1/ports` | `{https_port, http_port}` (0: off) | `covo ports` |
| `GET`, `POST /v1/groups` | `{name, parent_id}` | `covo group list`, `add` |
| `PATCH /v1/groups/{id}` | any of `{name, parent_id}` (0: top level) | `covo group rename`, `move` |
| `DELETE /v1/groups/{id}` | | `covo group remove` |
| `POST /v1/groups/{id}/enabled` | `{enabled}` | `covo group enable`, `disable` |
| `DELETE /v1/sites/{name}` | `?drop_database=true` | `covo site remove [--drop-database]` |
| `GET /v1/sites/{name}/logs/{access\|php}` | `?lines=N` | `covo site logs` |
| `DELETE /v1/sites/{name}/logs` | | `covo site logs --clear` |
| `GET`, `PATCH /v1/sites/{name}/php-settings` | `{"memory_limit": "1G"}`, `""` resets | `covo site php` |
| `GET /v1/sites/{name}/database` | | `covo db show` |
| `POST /v1/sites/{name}/database` | `{name, link}` | `covo db create`, `covo db link` |
| `DELETE /v1/sites/{name}/database` | `?drop=true` | `covo db unlink` |
| `GET /v1/databases` | | `covo db list` |
| `POST /v1/wordpress` | `{name, path, php, title, admin_user, admin_email, domain, aliases, group_id, php_settings}` | `covo wordpress` |
| `GET`, `POST /v1/php` | `{version}` | `covo php list`, `install` |
| `GET`, `PATCH /v1/php/{minor}/settings` | `{memory_limit, upload_max_filesize, max_execution_time, display_errors}` | `covo php settings` |
| `GET /v1/xdebug`, `PUT /v1/xdebug/{php}` | `{enabled}` | `covo php xdebug` |
| `GET /v1/domains`, `POST /v1/domains/{zone}/enable\|disable` | | `covo domains` |
| `GET /v1/mysql`, `POST /v1/mysql/enable\|disable` | `{port}` | `covo mysql` |
| `GET /v1/mysql/credentials` | | `covo mysql password` |
| `GET /v1/mail`, `POST /v1/mail/enable\|disable` | | `covo mail` |
| `POST /v1/adminer/login` | `{site}` (empty: root) | (app only) |
| `POST /v1/adminer/redeem` | `{token}` | (Covo's Adminer front file) |
| `POST /v1/trust`, `POST /v1/untrust` | | `covo trust`, `untrust` |
| `POST /v1/import/mamp` | `{dry_run, localhost}` | `covo import mamp` |

Responses are the same `data` as the matching CLI command with `--json`.
