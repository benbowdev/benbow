# Sites

A site is a folder served at `https://<name>.localhost:8443` (or another
domain under `.localhost`, `.test` or `.local`, see `domains.md`) with its own
PHP version.

```sh
covo site add shop --php 8.4 --path ~/Sites/shop   # --domain shop.test
covo site add shop --path ~/Sites/shop --create-folder --group Clients/Acme \
  --aliases www.shop.localhost --database new --ini memory_limit=1G
covo site list
covo site set shop --php 8.2                       # also --path, --domain
covo site set shop --aliases www.shop.localhost,shop.test   # "" clears
covo site set shop --group Clients                 # "" for the top level
covo site set shop --title "Shop – Acme"           # label shown in the list; "" clears
covo site logs shop --php -n 100                   # PHP errors; without --php: requests
covo site logs shop --clear
covo site disable shop                             # keep it, stop serving it
covo site enable shop
covo site archive shop                             # put it away: off, listed apart
covo site unarchive shop                           # back, and served again
covo site remove shop                              # the folder is never touched
covo site remove shop --drop-database --yes        # also delete its database
```

Changes apply at once when Covo is running; if applying fails, the previous
settings are restored. A folder without `index.php` or `index.html` needs
`--force` (the app asks).

Aliases are extra domains served by the same site, with the same rules as
the domain (reserved suffixes only, `.test`/`.local` once enabled). Every
domain and alias belongs to one site only; `.local` aliases are written to
`/etc/hosts` like domains. At most 20 per site.

A new site can start with everything at once: `--create-folder` makes a new
empty folder (it must be missing or empty), `--aliases`, `--group`,
`--database new` (own database and user) or `--database <existing>`
(connects as root), and repeated `--ini name=value` for its php.ini values.
If any part fails, everything is undone, including the folder it created.

## Groups

Groups organize the site list in folders nested to any depth (16 levels).
They only change the list: how a site is served never depends on its group.

```sh
covo group add Clients/Acme           # creates missing levels, like mkdir -p
covo group list
covo group rename Clients/Acme "Acme Corp"
covo group move Clients/Acme /        # into another group, / for the top level
covo group remove Clients             # its sites and groups move up one level
covo group disable Clients            # turn off every site inside, subgroups too
covo group enable Clients/Acme        # turn them back on (archived sites stay off)
covo group order Clients site:shop group:Clients/Acme site:blog
```

Paths are case-insensitive; names are unique within a group and cannot
contain `/`. The list is ordered by name until you order a level by hand:
`covo group order` (or drag and drop in the app) puts the named groups and
sites in that order, moving them into the level if needed; the others
follow, and new ones go last. Groups and sites mix freely (a level never
ordered shows groups first). In the app,
drop a row between two others to order (macOS draws the line), onto a
group to move it inside (or use Move To in the context menu); right-click a group for New Site Here, New Group
Inside, Turn All Sites On/Off, Rename and Remove. The switch on a group
row turns off every site inside when any is on, and turns them all on
otherwise.

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

API: `GET/POST /v1/sites` (`POST` also takes `create_folder`, `aliases`,
`group_id`, `php_settings`, `new_database`, `link_database`),
`PATCH /v1/sites/{name}` with any of
`php`, `path`, `domain`, `enabled`, `archived`, `aliases`, `group_id`, `title`, `force`,
`GET/POST /v1/groups`, `PATCH /v1/groups/{id}` (`name`, `parent_id`),
`DELETE /v1/groups/{id}`, `PUT /v1/list-order` (`group`, `items`), `DELETE /v1/sites/{name}`, `GET /v1/sites/{name}/logs/{access|php}?lines=N`
and `DELETE /v1/sites/{name}/logs`.
