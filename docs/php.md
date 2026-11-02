# PHP

Covo runs static PHP-FPM builds, one process per minor version, shared by
every site on that version.

```sh
covo php list
covo php install 8.3                 # also happens when a site needs it
covo php settings 8.4                # show php.ini settings
covo php settings 8.4 memory_limit=1G upload_max_filesize=256M max_execution_time=0 display_errors=off
```

| Setting | Default | Values |
|---|---|---|
| `memory_limit` | 512M | size (`K`, `M`, `G`) or `-1` (no limit) |
| `upload_max_filesize` | 128M | size; `post_max_size` always follows it |
| `max_execution_time` | 300 | seconds, `0` = no limit, max 86400 |
| `display_errors` | on | on/off (also sets `display_startup_errors`) |

Changes apply to every site on that version: PHP-FPM restarts at once if
Covo is running. Values are validated before anything is saved, so nothing
else can be written into php.ini. Errors always go to the PHP error log
(`log_errors = On`, `error_reporting = E_ALL`).

## Per site

```sh
covo site php shop                                  # effective values, and where each comes from
covo site php shop memory_limit=1G display_errors=off
covo site php shop memory_limit=                    # back to the PHP version's value
```

A site's own values sit on top of its PHP version's. They reach PHP-FPM as
the FastCGI `PHP_VALUE` of that site's requests, together with its error
log; every setting is always sent, because PHP-FPM keeps a `PHP_VALUE` in the
worker for later requests and a site must never inherit another's. In the
app: site detail → Advanced → PHP settings. API: `GET` and
`PATCH /v1/sites/{name}/php-settings` (`{"memory_limit": "1G"}`, `""` resets).

In the app: Settings → PHP. API: `GET` and `PATCH /v1/php/{minor}/settings`.

Xdebug: see `xdebug.md`.
