# Sites

A site is a folder served at `https://<name>.localhost:8443` (or another
domain under `.localhost`, `.test` or `.local`, see `domains.md`) with its own
PHP version.

```sh
covo site add shop --php 8.4 --path ~/Sites/shop   # --domain shop.test
covo site list
covo site set shop --php 8.2                       # also --path, --domain
covo site set shop --aliases www.shop.localhost,shop.test   # "" clears
covo site logs shop --php -n 100                   # PHP errors; without --php: requests
covo site logs shop --clear
covo site disable shop                             # keep it, stop serving it
covo site enable shop
covo site archive shop                             # put it away: off, listed apart
covo site unarchive shop                           # back, and served again
covo site remove shop                              # the folder is never touched
```

Changes apply at once when Covo is running; if applying fails, the previous
settings are restored. A folder without `index.php` or `index.html` needs
`--force` (the app asks).

Aliases are extra domains served by the same site, with the same rules as
the domain (reserved suffixes only, `.test`/`.local` once enabled). Every
domain and alias belongs to one site only; `.local` aliases are written to
`/etc/hosts` like domains. At most 20 per site.

## Logs

Each site has its own logs under `logs/sites/<name>/` in the Covo folder:
`access.log` (requests, written by Caddy, rolled at 5 MiB) and
`php-error.log` (errors, warnings and notices of that site only, rotated at
10 MiB on start). `covo site logs` condenses requests to
`date time  status  method  host/path  duration`. Removing a site deletes
its logs (never its folder). In the app: the Logs button in the site
detail, refreshed every 2 seconds.

An archived site is off and listed apart (the app shows it under
"Archived", collapsed); its folder, database, aliases and settings are kept.
It cannot be turned on until it is unarchived.

A disabled site stays in the list but Caddy does not serve it, and a PHP
version used only by disabled sites is not started.

API: `GET/POST /v1/sites`, `PATCH /v1/sites/{name}` with any of
`php`, `path`, `domain`, `enabled`, `archived`, `aliases`, `force`, `DELETE /v1/sites/{name}`, `GET /v1/sites/{name}/logs/{access|php}?lines=N`
and `DELETE /v1/sites/{name}/logs`.
