---
name: benbow
description: Use when working on a local PHP, WordPress or Laravel site served by Benbow (addresses like https://shop.localhost:8443, a project folder Benbow knows, or the user mentions Benbow). Explains Benbow's agent sessions, which tools to change and check the site with, and to show the session diff before finishing.
---

# Working on a Benbow site

Benbow is the user's local PHP/WordPress/Laravel environment on this Mac.
Its MCP server (`benbow`) gives you the sites, their PHP, databases, logs
and mail. Benbow keeps the site safe while you work: you do not need to
make backups yourself.

## Before you change anything

1. Call `benbow_status`: Benbow running, the sites with their addresses,
   databases, mail.
2. Call `agent_permissions`. You can always read. You may change a site
   only if the user allowed it, or while a "Work with an agent" session
   gives it to you (`temporary`). If not allowed, ask the user to allow it
   in Benbow (Settings → AI Agents) instead of working around it.
3. Call `session_status` for the site. An agent session is open while you
   work: Benbow saved the files and the database when it started, and the
   user will **keep** your changes or **undo them all** in Benbow. You
   cannot close it yourself, and you should not try.

## While you work

- Edit files as usual. For WordPress, Laravel and Composer use Benbow's
  `wp`, `artisan` and `composer` tools: they run with the site's own PHP
  version and database. Benbow takes a checkpoint before risky commands
  (plugin or core updates, `db import`, `search-replace`, migrations).
- Use `dry_run: true` first when a change is large or you are unsure.
- Never print or copy secrets (`wp-config.php` salts, `.env` passwords).
  `session_diff` masks them for you.

## Check your work

- `fetch_page` with the site and a path: status, redirects, the page's
  text and the PHP errors, warnings and deprecations of that very request.
  Fix warnings and deprecations you introduced.
- `site_logs` for the PHP error log and the request log.
- `mail_messages` and `read_mail` when a form or a notification sends
  mail: Benbow catches it locally (nothing leaves the Mac); check the
  recipient, the subject and the links.
- `query_database` for one read-only SELECT to verify data. Changes go
  through `wp` or `artisan`, never through SQL.

## Before you say you are done

Call `session_diff` for the site and show the user, briefly, what changed:
files added, modified and deleted, and the database tables with rows
inserted, updated and deleted. Then remind them that they can keep the
changes or undo everything in Benbow (the site's banner, or
`benbow agent session keep|revert <site>`).
