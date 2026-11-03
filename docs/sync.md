# Production and staging

```sh
covo remote set shop --kind production --host example.com --user deploy \
  --path /var/www/shop --url https://shop.example.com            # SSH (default)
covo remote set shop --kind staging --name stage --transport sftp --host … --password …
covo remote show shop
covo pull shop                  # from "production": database + uploads
covo pull shop stage --dry-run  # what would change
covo push shop stage            # staging only; asks first (--yes)
covo remote remove shop stage
```

A **remote** is a copy of the site on a server: `production` (pull only) or
`staging` (pull and push). Covo never pushes to production.

| Transport | Login | Files | Database |
|---|---|---|---|
| `ssh` | `/usr/bin/ssh`: your keys, ssh-agent and `~/.ssh/config` | `/usr/bin/rsync` (openrsync) | the server's `wp db export`/`wp db import` when it has wp-cli, else `mysqldump`/`mysql` with the credentials of the remote `wp-config.php` or `.env` |
| `sftp` | same, for hosts that give SFTP but no shell | SFTP (`ssh -s sftp`) | the PHP bridge |
| `ftps`, `ftp` | user and password | FTP (`ftp` is unencrypted: Covo warns) | the PHP bridge |

Covo never creates or changes `~/.ssh`. New host keys are accepted the first
time (`StrictHostKeyChecking=accept-new`), a changed one is refused. A
password (`--password`, asked on the terminal) is kept in the login keychain
(`dev.getcovo.remote`); ssh gets it through `SSH_ASKPASS` = `covo askpass`,
never as an argument. Database credentials read on the server are used for
that run only: over SSH they reach `mysqldump` on its stdin (or a private
file removed at once, when stdin carries the SQL).

## Pull

1. A snapshot of the site (`pre-pull`): restore it to undo the pull.
2. Files: only the synced folders (default `wp-content/uploads`,
   `storage/app/public`, or the whole project for other sites), never `.git`,
   `node_modules`, `.DS_Store`, `cache` (`--exclude`). Nothing local is
   deleted.
3. Database: the remote dump replaces the site's database (created if the site
   has none), streamed without touching the disk; `DEFINER` clauses removed.
   A dump that stops half way fails the pull (the snapshot restores).
4. WordPress: the remote address (the real `home` of the pulled database) is
   replaced with the local one by `wp search-replace` (serialized data safe).

## Push (staging only)

A snapshot of the local site first, then the synced folders are uploaded
(changed files only) and the database replaces the staging one, with the
local address replaced by the staging one: `wp search-replace --export`
writes the converted dump without changing the local database.

## The PHP bridge (hosts without SSH)

Covo uploads `covo-bridge-<random>.php` into the remote web folder (`--path`,
plus `--webroot public` for Laravel) and calls it over HTTPS with a 256-bit
token in a header. It reads the database settings from `wp-config.php` or
`.env` next to it, answers only requests with the token, stops working 10
minutes after it was written and then deletes itself; Covo deletes it as soon
as it is done, whatever happens. Dumps are streamed (gzip, 200 rows per
`INSERT`); imports are sent with their size. Some hosts block new PHP files
(WAF, mod_security): Covo then says the bridge did not answer.

`--dry-run` shows the files that would change and what would happen to the
database, changing nothing (no snapshot either). PostgreSQL sites cannot be
synced yet.

In the app: the site's Advanced section → Production and staging: remotes,
Pull… and Push… (each shows what will change first).

API: `GET /v1/sites/{name}/remotes`, `PUT`, `DELETE
/v1/sites/{name}/remotes/{remote}` (`kind`, `transport`, `host`, `port`,
`user`, `path`, `url`, `webroot`, `paths`, `excludes`, `password`),
`POST /v1/sites/{name}/remotes/{remote}/pull|push` (`dry_run`, `no_files`,
`no_database`).
