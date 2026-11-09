# Moving from Covo

Benbow was called Covo until November 2026. Sparkle cannot update an app
whose name and bundle ID both change, so Covo does not turn into Benbow by
itself: install Benbow, and it moves Covo's installation over.

```sh
benbow migrate covo --dry-run   # what would happen; changes nothing
benbow migrate covo             # do it
benbow migrate covo --finish    # only the clean-up, if it was held back
```

In the app, Benbow offers it on its first start when it finds Covo.

## What happens

1. **Covo stops**: its launchd agents (`dev.getcovo.*`), the core included.
   Covo.app must be closed first, or it starts Covo again in a new, empty
   folder: the app quits it, the command refuses to run while it is open.
2. **The folder moves**: `~/Library/Application Support/covo` becomes
   `~/Library/Application Support/benbow` with one rename. Sites'
   databases, snapshots, logs and downloaded components come along without
   being copied. `covo.db` becomes `benbow.db`, and the absolute paths
   saved in it (PHP versions, snapshots, migration backups) are rewritten.
   An empty Benbow folder already there is moved aside first. Components
   Covo downloaded (PHP, Caddy, MySQL…) are marked as Benbow's, so nothing
   downloads again.
3. **Keychain items are copied** from `dev.getcovo.{mysql,postgres,remote,
   github,appenv}` to `dev.benbow.*`, with the same accounts: database
   passwords, server passwords, the GitHub token, apps' variables.
   Covo's read-only MySQL users are not copied: Benbow makes its own the
   first time an agent queries a database.
4. **A new local CA**: the CA's name is inside its certificate, so Benbow
   has its own, "Benbow Local CA", with the same limits (`.localhost`,
   `.test`, `.local` only, no IP addresses). macOS asks for your password
   to trust it. Caddy issues new site certificates with it. A phone that
   had Covo's profile needs Benbow's (Open on Phone shows how).
5. **`.test`**, if it was on: `/etc/resolver/test` is rewritten for
   Benbow, after one administrator password.
6. **Benbow starts** (if Covo was running) and asks every PHP site for its
   home page: any HTTP answer counts.
7. **Finish**: Covo's keychain items are deleted, Covo's CA is untrusted
   and removed, Covo's read-only MySQL users are dropped, and AI agents
   connected to Covo's MCP server (`covo`) get Benbow's (`benbow`) instead,
   with a backup of their configuration. Then Covo.app can go to the
   Trash.

If a site does not answer in step 6, step 7 waits: Covo's keychain items
and CA stay until you run `benbow migrate covo --finish`.

## If something goes wrong

- Steps 1–3 are undone on failure: the folder moves back, Covo's database
  is put back exactly as it was (Benbow may have upgraded its copy), the
  copied keychain items are deleted. Open Covo and it starts as before.
- From step 4 on, Benbow owns the installation. A cancelled password
  dialog only leaves a warning: run `benbow trust` (or Trust in the app)
  for the CA, `benbow domains enable test` for `.test`.
- The migration refuses to run when Benbow already has sites.

Covo's database as it was and its CA root stay in
`data/covo-migration/` until the finish step.
