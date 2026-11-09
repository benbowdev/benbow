# Snapshots

```sh
benbow snapshot create shop --note "before the plugin update"
benbow snapshot list shop
benbow snapshot restore shop 20261103-124501.123      # asks first (--yes)
benbow snapshot delete shop 20261103-124501.123
benbow snapshot keep 20                                # per site (default 10)
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
- the **database**, if the site has one: `mysqldump --single-transaction`
  (gzip) or `pg_dump -Fc`.

**Restore** first takes a `pre-restore` snapshot of the current state, then
moves the project folder aside, clones the snapshot back, moves the folders
left out back from the old folder, recreates the database and loads
the dump (PostgreSQL objects are owned by the site's own role). If any step
fails, the folder and the database are put back. If the restore is cut
short (crash, kill, reboot), a journal in `data/restores` lets the next
daemon start or restore put the folder and the database back the same way.
A restore holds Benbow's lock, so other commands wait for it. Restoring the
pre-restore snapshot undoes a restore.

Only the newest snapshots are kept (10 per site by default, `benbow snapshot
keep`); a snapshot being restored is never pruned. Removing a site deletes
its snapshots; `benbow uninstall` deletes them all. Snapshots are also taken
before an SSH pull, and at the start of an AI agent's session and before
its risky changes (`agents.md`): a session's snapshots do not count in the
limit while it is open.

In the app: the site's Snapshots tool; Settings → General →
Snapshots for how many to keep.

API: `GET`, `POST /v1/sites/{name}/snapshots` (`note`),
`POST /v1/sites/{name}/snapshots/{id}/restore`,
`DELETE /v1/sites/{name}/snapshots/{id}`, `GET`, `PUT /v1/snapshots/keep`
(`keep`).
