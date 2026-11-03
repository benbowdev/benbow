# Importing from other tools

```
covo import mamp --dry-run     # what would be created; changes nothing
covo import mamp               # keep .local names (needs `covo domains enable local`)
covo import mamp --localhost   # serve them as <name>.localhost instead
covo import local|herd|valet|xampp [--dry-run] [--site shop.local]…
covo import mamp --migrate --site shop.local          # also move the database (below)
covo import undo shop
```

Sources, all only read (every field optional: none of these formats is
documented):

| Source | Where | Sites | Database |
|---|---|---|---|
| MAMP PRO | `~/Library/Application Support/appsolute/MAMP PRO/settings6.plist` (a copy) | virtual hosts | MAMP's MySQL, socket `/Applications/MAMP/tmp/mysql/mysql.sock` or 127.0.0.1:3306, root/root |
| Local | `~/Library/Application Support/Local/sites.json` | each site, served from `app/public` | one MySQL per site (port and socket in `sites.json`), only while the site runs in Local |
| Laravel Herd | `~/Library/Application Support/Herd/config/valet/` | parked folders and linked sites (`Sites/`), isolated PHP from `Nginx/<site>` | Herd Pro's, or your own |
| Laravel Valet | `~/.config/valet/` | same | usually Homebrew's MySQL on 127.0.0.1:3306 (only read; Covo never runs `brew`) |
| XAMPP | `/Applications/XAMPP/xamppfiles/etc/extra/httpd-vhosts.conf` (if `httpd.conf` includes it) | virtual hosts, XAMPP's one PHP | MariaDB on 127.0.0.1:3306, root without password |

Herd and Valet sites keep their `.test` names (`covo domains enable test`) or
become `<name>.localhost` with `--localhost`. A site without a PHP version in
its source gets the newest fully supported one, with a warning.

## Assisted migration

Without `--migrate` the site uses its folder as is: its database settings
stay those of the source tool. With `--migrate`, for each new WordPress or
Laravel site, in one step:

1. **Database**: copied from the source's MySQL with `mysqldump
   --single-transaction` (read only; the source's MySQL must be running —
   Covo never starts or stops it), streamed into Covo's MySQL without
   touching the disk; `DEFINER` clauses are removed. The source credentials
   are those in `wp-config.php`/`.env` (or `--source-user` and
   `--source-password`, asked on the terminal), used for that copy only and
   never stored. `--dump file.sql[.gz]` loads a dump instead (for one site),
   e.g. from `~/Documents/MAMP backup/`. If Covo already has a
   database of that name: `--existing use` (default), `recopy` (after a gzip
   backup in the Covo folder) or `skip`.
2. **Own user**: the site gets a MySQL user of the database's name, password
   in the keychain, like `covo db create`.
3. **Configuration**: `wp-config.php` (or `.env`) is backed up to
   `data/backups/<site>/` in the Covo folder, then only `DB_NAME`,
   `DB_USER`, `DB_PASSWORD` and `DB_HOST` change (Laravel: the `DB_*` keys
   and `APP_URL`). If one of the four `define()` lines cannot be found
   exactly once, nothing changes. Other PHP sites are not touched: the
   values to set are shown.
4. **Address**: WordPress's real `home` is read from the copied database and
   replaced with the Covo address (both schemes) by `wp search-replace`,
   which handles serialized data; `guid` columns are left alone.

If a step fails, everything that site's migration did is undone; other
sites carry on. **The project folder stays shared with the source tool**:
after a migration the site works from Covo, and the source tool's copy no
longer reaches its database (it would show old data). `covo import undo
<site>` (or Undo Migration in the site's Advanced section) restores the
configuration from the backup and deletes the database Covo copied, with
its user; the site stays.

In the app: File → Import from Another Tool…, with the source, the sites to
import and "Also migrate".

MAMP PRO is never modified and can keep running next to Covo. Covo copies
`~/Library/Application Support/appsolute/MAMP PRO/settings6.plist` into its own
`tmp/` folder, converts the copy with `/usr/bin/plutil` and deletes it: the
original is only read once, by the copy.

## Mapping

| MAMP PRO | Covo |
|---|---|
| `shop.local` | site `shop`, domain `shop.local` (or `shop.localhost` with `--localhost`) |
| `blog.client.local` | site `blog-client`, domain `blog.client.local` |
| a name without suffix (`intranet`) | `intranet.local`, with a warning: Covo only serves reserved suffixes |
| `localhost` (MAMP's default host) | skipped |
| document root | the same folder, used in place: nothing is copied or moved |
| PHP 8.x | the same minor version (8.1.13 → 8.1, 8.2.0 → 8.2) |
| PHP 5.6–7.4 | the same version on Apple Silicon (covo-binaries' legacy builds, macOS 15+); elsewhere PHP 8.1, with a warning |
| SSL | always on: every Covo site is HTTPS |
| aliases | imported, mapped like the server name; one already used by another site is skipped with a warning |
| databases | without `--migrate`: a new site is linked to its database when MAMP linked exactly one and a database of that name already exists on Covo's MySQL (connects as root); with `--migrate`: copied (see above) |

Hosts are skipped when their folder does not exist, when Covo refuses the
folder (your home, a system folder, Covo's own), when the name is not a valid
host name, or when a Covo site already uses the domain. A second import only
adds what is missing.

All sites of one import are created together: if one fails, none is kept.

Old PHP versions are end of life upstream: `covo php check <site>` lists what
would break on a newer one.
