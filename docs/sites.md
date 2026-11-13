# Sites

A site is a folder served at `https://<name>.localhost:8443` (or another
domain under `.localhost`, `.test` or `.local`, see `domains.md`) with its own
PHP version. A site can also be an app: a dev server in any language behind
the same kind of domain, see `apps.md`.

```sh
benbow site add shop --php 8.4 --path ~/Sites/shop   # --domain shop.test
benbow site add shop --path ~/Sites/shop --create-folder --group Clients/Acme \
  --aliases www.shop.localhost --database new --ini memory_limit=1G
benbow site list
benbow site set shop --php 8.2                       # also --path, --domain
benbow site set shop --aliases www.shop.localhost,shop.test   # "" clears
benbow site set shop --group Clients                 # "" for the top level
benbow site set shop --title "Shop – Acme"           # label shown in the list; "" clears
benbow site logs shop --php -n 100                   # PHP errors; without --php: requests
benbow site logs shop --clear
benbow site disable shop                             # keep it, stop serving it
benbow site enable shop
benbow site archive shop                             # put it away: off, listed apart
benbow site unarchive shop                           # back, and served again
benbow site remove shop                              # the folder is never touched
benbow site remove shop --drop-database --yes        # also delete its database
benbow site clone shop shop-copy                     # folder + database + address
benbow site clone shop shop-82 --same-folder --php 8.2   # same folder, new name
```

Changes apply at once when Benbow is running; if applying fails, the previous
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

## Clone

`benbow site clone <site> <new>` copies a site under a new name:

1. The project folder is cloned with APFS into `--folder` (default: next to
   the source's, named after the new site; it must not exist). Everything is
   copied, `.git` and `node_modules` included; a clone takes no space until
   files change (another file system: a plain copy).
2. The database is dumped and loaded into a new one with its own user
   (MySQL or PostgreSQL).
3. The copy's `wp-config.php` (database constants, and `WP_HOME`/`WP_SITEURL`
   when it sets them) or `.env` (`APP_URL`, `DB_DATABASE`, `DB_USERNAME`,
   `DB_PASSWORD`) points at it. Without either, the result says which
   database to use.
4. WordPress: the source's address is replaced with the new one in the
   copied database (`wp search-replace`, serialized data safe).

The new site gets the source's PHP version (`--php` to change it), group and
php.ini values. The source is only read. If any step fails, the new site, its
database and the folder are removed. Apps cannot be cloned yet. A
`wp-config.php` above the site's folder (which WordPress allows) is refused:
the copy would share it.

`--same-folder` copies nothing: the source's folder is served under the new
name too, e.g. to try another PHP version. Files and database stay shared.

## Groups

Groups organize the site list in folders nested to any depth (16 levels).
They only change the list: how a site is served never depends on its group.

```sh
benbow group add Clients/Acme           # creates missing levels, like mkdir -p
benbow group list
benbow group rename Clients/Acme "Acme Corp"
benbow group move Clients/Acme /        # into another group, / for the top level
benbow group remove Clients             # its sites and groups move up one level
benbow group disable Clients            # turn off every site inside, subgroups too
benbow group enable Clients/Acme        # turn them back on (archived sites stay off)
benbow group order Clients site:shop group:Clients/Acme site:blog
```

Paths are case-insensitive; names are unique within a group and cannot
contain `/`. The list is ordered by name until you order a level by hand:
`benbow group order` (or drag and drop in the app) puts the named groups and
sites in that order, moving them into the level if needed; the others
follow, and new ones go last. Groups and sites mix freely (a level never
ordered shows groups first). In the app,
drop a row between two others to order (macOS draws the line), onto a
group to move it inside (or use Move To in the context menu); right-click a group for New Site Here, New Group
Inside, Turn All Sites On/Off, Rename and Remove. The switch on a group
row turns off every site inside when any is on, and turns them all on
otherwise.

## Logs

Each site has its own logs under `logs/sites/<name>/` in the Benbow folder:
`access.log` (requests, written by Caddy, rolled at 5 MiB) and
`php-error.log` (errors, warnings and notices of that site only, rotated at
10 MiB on start). `benbow site logs` condenses requests to
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
