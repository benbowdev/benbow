# Importing from other tools

```
benbow import mamp --dry-run     # what would be created; changes nothing
benbow import mamp               # keep .local names (needs `benbow domains enable local`)
benbow import mamp --localhost   # serve them as <name>.localhost instead
benbow import local|herd|valet|xampp [--dry-run] [--site shop.local]…
benbow import mamp --migrate --site shop.local          # also move the database (below)
benbow import archive ~/Downloads/shop.zip [--name shop] [--php 7.4] [--dry-run]  # a site archive (below)
benbow import undo shop
```

Sources, all only read (every field optional: none of these formats is
documented):

| Source | Where | Sites | Database |
|---|---|---|---|
| MAMP PRO | `~/Library/Application Support/appsolute/MAMP PRO/settings6.plist` (a copy) | virtual hosts | MAMP's MySQL, socket `/Applications/MAMP/tmp/mysql/mysql.sock` or 127.0.0.1:3306, root/root |
| Local | `~/Library/Application Support/Local/sites.json` | each site, served from `app/public` | one MySQL per site (port and socket in `sites.json`), only while the site runs in Local |
| Laravel Herd | `~/Library/Application Support/Herd/config/valet/` | parked folders and linked sites (`Sites/`), isolated PHP from `Nginx/<site>` | Herd Pro's, or your own |
| Laravel Valet | `~/.config/valet/` | same | usually Homebrew's MySQL on 127.0.0.1:3306 (only read; Benbow never runs `brew`) |
| XAMPP | `/Applications/XAMPP/xamppfiles/etc/extra/httpd-vhosts.conf` (if `httpd.conf` includes it) | virtual hosts, XAMPP's one PHP | MariaDB on 127.0.0.1:3306, root without password |

Herd and Valet sites keep their `.test` names (`benbow domains enable test`) or
become `<name>.localhost` with `--localhost`. A site without a PHP version in
its source gets the newest fully supported one, with a warning.

## Assisted migration

Without `--migrate` the site uses its folder as is: its database settings
stay those of the source tool. With `--migrate`, for each new WordPress or
Laravel site, in one step:

1. **Database**: copied from the source's MySQL with `mysqldump
   --single-transaction` (read only; the source's MySQL must be running —
   Benbow never starts or stops it), streamed into Benbow's MySQL without
   touching the disk; `DEFINER` clauses are removed. The source credentials
   are those in `wp-config.php`/`.env` (or `--source-user` and
   `--source-password`, asked on the terminal), used for that copy only and
   never stored. `--dump file.sql[.gz]` loads a dump instead (for one site),
   e.g. from `~/Documents/MAMP backup/`. If Benbow already has a
   database of that name: `--existing use` (default), `recopy` (after a gzip
   backup in the Benbow folder) or `skip`.
2. **Own user**: the site gets a MySQL user of the database's name, password
   in the keychain, like `benbow db create`.
3. **Configuration**: `wp-config.php` (or `.env`) is backed up to
   `data/backups/<site>/` in the Benbow folder, then only `DB_NAME`,
   `DB_USER`, `DB_PASSWORD` and `DB_HOST` change (plus the address
   defines, step 4) (Laravel: the `DB_*` keys
   and `APP_URL`). If one of the four `define()` lines cannot be found
   exactly once, nothing changes. Other PHP sites are not touched: the
   values to set are shown.
4. **Address**: WordPress's real `home` is read from the copied database and
   replaced with the Benbow address (both schemes) by `wp search-replace`,
   which handles serialized data; `guid` columns are left alone. If
   `wp-config.php` sets `WP_HOME` or `WP_SITEURL` (they win over the
   database, so the site would keep redirecting to the old address), they
   change too, in the same edit as the database settings: `WP_HOME` to the
   Benbow address, `WP_SITEURL` keeping its path under the old home (`/wp`).
   A define set from code, written more than once, or pointing outside the
   old address is left alone and listed as a warning to check by hand
   (`warnings` in `--json`).

If a step fails, everything that site's migration did is undone; other
sites carry on. **The project folder stays shared with the source tool**:
after a migration the site works from Benbow, and the source tool's copy no
longer reaches its database (it would show old data). `benbow import undo
<site>` (or Undo Migration in the site's Advanced section) restores the
configuration from the backup and deletes the database Benbow copied, with
its user; the site stays.

In the app: File → Import from Another Tool…, with the source, the sites to
import and "Also migrate".

MAMP PRO is never modified and can keep running next to Benbow. Benbow copies
`~/Library/Application Support/appsolute/MAMP PRO/settings6.plist` into its own
`tmp/` folder, converts the copy with `/usr/bin/plutil` and deletes it: the
original is only read once, by the copy.

## Mapping

| MAMP PRO | Benbow |
|---|---|
| `shop.local` | site `shop`, domain `shop.local` (or `shop.localhost` with `--localhost`) |
| `blog.client.local` | site `blog-client`, domain `blog.client.local` |
| a name without suffix (`intranet`) | `intranet.local`, with a warning: Benbow only serves reserved suffixes |
| `localhost` (MAMP's default host) | skipped |
| document root | the same folder, used in place: nothing is copied or moved |
| PHP 8.x | the same minor version (8.1.13 → 8.1, 8.2.0 → 8.2) |
| PHP 5.6–7.4 | the same version on Apple Silicon (benbow-binaries' legacy builds, macOS 15+); elsewhere PHP 8.1, with a warning |
| SSL | always on: every Benbow site is HTTPS |
| aliases | imported, mapped like the server name; one already used by another site is skipped with a warning |
| databases | without `--migrate`: a new site is linked to its database when MAMP linked exactly one and a database of that name already exists on Benbow's MySQL (connects as root); with `--migrate`: copied (see above) |

Hosts are skipped when their folder does not exist, when Benbow refuses the
folder (your home, a system folder, Benbow's own), when the name is not a valid
host name, or when a Benbow site already uses the domain. A second import only
adds what is missing.

All sites of one import are created together: if one fails, none is kept.

Old PHP versions are end of life upstream: `benbow php check <site>` lists what
would break on a newer one.

## Site archives

`benbow import archive <file>` (File → Import Site Archive…, or drop a .zip
on the window) creates a site from a `.zip`, `.tar.gz` or `.tar` holding the
site's folder and, anywhere inside, a `.sql` or `.sql.gz` dump: Local's
exports, cPanel and host backups, Duplicator archives, a zip you made. The
archive is only read.

- The site's folder is the shallowest one holding `wp-includes/version.php`
  (WordPress), else `artisan` (Laravel), else `index.php` (or `index.html`).
  It goes to `--folder`, default `~/Sites/<name>` (missing or empty). The
  name comes from the archive's file name unless `--name` is given.
- The largest dump is loaded into a new database with its own user
  (`DEFINER` and `USE` lines removed); the others are listed. The dump never
  lands in the site's folder, where anyone reaching the site could download
  it.
- WordPress: `wp-config.php` (made from `wp-config-sample.php` when the
  archive has none) gets the database settings, and the old address read
  from the dump is replaced (`wp search-replace`, serialized data safe).
  Laravel: `.env` (or a copy of `.env.example`) gets `APP_URL` and `DB_*`.
  Other sites: the result says which database to use.
- Without `--php`, an old WordPress gets a version it runs on: before 4.0
  PHP 5.6, before 5.3 PHP 7.4, before 6.3 PHP 8.1, else 8.4.
- Names are checked (no absolute paths, no `..`), links and special files
  are skipped, macOS metadata (`__MACOSX`, `.DS_Store`) is left out; at most
  a million files and 100 GiB unpacked. Unpacking happens in Benbow's
  temporary folder. If any step fails, the site, its database and the folder
  are removed.

`--engine mysql57` loads the dump into the optional MySQL 5.7 server
(`mysql.md`) instead of 8.4. `--dry-run` reads only the list of files (and
WordPress's version) and says what would be created.
