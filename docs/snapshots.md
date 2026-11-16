# Snapshots

```sh
benbow snapshot create shop --note "before the plugin update"
benbow snapshot list shop
benbow snapshot restore shop 20261103-124501.123      # asks first (--yes)
benbow snapshot restore shop 20261103-124501.123 --php  # also its PHP version and php.ini values
benbow snapshot delete shop 20261103-124501.123
benbow snapshot keep 20                                # per site (default 10)
benbow snapshot usage [shop]                           # space really taken, one site or all
benbow snapshot prune --older-than 30d [--site shop] [--dry-run]
benbow snapshot export shop [20261103-124501.123] [--out shop.zip]  # newest if no id
```

A snapshot saves, under `data/snapshots/<site>/<id>/` in the Benbow folder:

- the **project folder** (the Laravel project, or the site's folder), cloned
  with APFS `clonefile(2)`: it takes no space until files change. `.git`,
  `node_modules` and the folders dev servers generate (`.venv`, `venv`,
  `__pycache__`, `.pytest_cache`, `.mypy_cache`, `.ruff_cache`, `.next`,
  `.nuxt`, `.svelte-kit`, `.astro`, `.turbo`, `.parcel-cache`), at any
  depth, are left out, so a restore never rewinds git history or installed
  packages. On a volume without clones
  (not APFS, or another disk than the Benbow folder's) the files are copied;
- a **file list** (`files.tsv`): kind, size and path of every saved file
  and symlink. Sizes, not hashes, so a snapshot of a big site stays quick;
- the **database**, if the site has one: `mysqldump --single-transaction`
  (gzip) or `pg_dump -Fc`, with the server's version recorded
  (`database_version`), so a dump that fails to load says where it came
  from.

**Restore** first checks the snapshot is whole (its files are there and
match its file list, the gzip dump reads to the end with a good CRC, a
PostgreSQL dump is a `pg_dump` archive; snapshots older than the file list
skip that part) and refuses before touching the site if not. It then
takes a `pre-restore` snapshot of the current state, then
moves the project folder aside, clones the snapshot back, moves the folders
left out back from the old folder, recreates the database and loads
the dump (PostgreSQL objects are owned by the site's own role). If any step
fails, the folder and the database are put back. If the restore is cut
short (crash, kill, reboot), a journal in `data/restores` lets the next
daemon start or restore put the folder and the database back the same way.
Once files and database are restored the journal is marked done before the
old folder is deleted, so a restore cut short at that point is finished
(the rest of the old folder deleted), never rolled back onto a
half-deleted folder. If loading the database fails and putting the old one
back fails too, the error says so and names the dump to load by hand.
A restore holds Benbow's lock, so other commands wait for it. Restoring the
pre-restore snapshot undoes a restore.

Only the newest snapshots are kept (10 per site by default, `benbow snapshot
keep`); a snapshot being restored is never pruned. Removing a site deletes
its snapshots; `benbow uninstall` deletes them all. Snapshots are also taken
before an SSH pull, and at the start of an AI agent's session and before
its risky changes (`agents.md`): a session's snapshots do not count in the
limit while it is open.

A snapshot also records the site's PHP version and its own php.ini values.
A restore puts back files and database only; when the PHP saved differs from
the site's, the result says so (`php_differs`) and `--php` (in the app, a
switch in the restore dialog) brings the version and values back too.
Older snapshots have no PHP saved.

## Export

`benbow snapshot export <site> [id]` writes a snapshot (the newest without
an id) as a zip, to keep, send or move to another Mac or a host:

- `<site>/`: the project folder as saved (without `.git`, `node_modules`
  and generated folders); symlinks stay symlinks. Already compressed files
  (images, video, archives, fonts) are stored, the rest deflated;
- `<site>.sql`: the MySQL dump as plain SQL, ready for phpMyAdmin, `mysql`
  or a host's import. PostgreSQL: `<site>.pgdump`, a `pg_dump` archive for
  `pg_restore`;
- `benbow-snapshot.json`: site, snapshot id, date, kind, note, PHP version
  and php.ini values, database engine and server version.

`benbow import archive` reads it back as a new site (`import.md`). The
snapshot is checked first (as for a restore) and cloned under Benbow's lock,
then zipped without it, so other commands do not wait and pruning cannot
delete it halfway. The zip is written as `<name>.zip.partial` and renamed
when complete; an existing file is never overwritten.

In the app: the export button on each snapshot in the site's Snapshots
tool, with a save panel. The daemon writes the zip in Benbow's
`tmp/exports` and the app moves it where the panel points: the daemon,
started by launchd, has no access to Desktop, Documents or Downloads
without its own permission prompt. Exports nobody moved are deleted after
an hour.

## Space

Files in a snapshot are APFS clones of the site: they share its blocks until
either side changes. `du` counts them in full, so a few snapshots of a big
site look like tens of gigabytes. `benbow snapshot usage` shows both sizes:
apparent (what `du` says) and real, the bytes no clone shares (APFS's
private size of each file, `getattrlist` `ATTR_CMNEXT_PRIVATESIZE`), which is
what deleting the snapshot gives back: its database dump plus the files that
changed since. Blocks two snapshots share with each other but no longer with
the site count for neither, so deleting several can free a little more than
their sum. Measuring walks every file; the daemon reuses a result for 10
minutes.

`benbow snapshot prune --older-than <age>` (30d, 2w, 12h) deletes older
snapshots of one site or all, after asking (`--yes`), and reports the space
freed; `--dry-run` only lists them. Snapshots of an open agent session are
never pruned.

In the app: the site's Snapshots tool (real size per snapshot, restore with
or without PHP); Settings → Snapshots: every snapshot with its real size,
the total, "Delete snapshots older than…" (lists what goes first), and how
many to keep.

API: `GET`, `POST /v1/sites/{name}/snapshots` (`note`),
`POST /v1/sites/{name}/snapshots/{id}/restore`,
`POST /v1/sites/{name}/snapshots/{id}/export` (`path`: absolute, ending
in `.zip`; empty = Benbow's `tmp/exports`),
`DELETE /v1/sites/{name}/snapshots/{id}`, `GET`, `PUT /v1/snapshots/keep`
(`keep`), `GET /v1/snapshots/usage?site=`, `POST /v1/snapshots/prune`
(`site`, `older_than_days`, `dry_run`); restore takes `{php}`.
