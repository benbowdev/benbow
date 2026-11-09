# WordPress

## New WordPress site

```sh
benbow wordpress blog                       # ~/Sites/blog, PHP 8.4, admin "admin"
benbow wordpress blog --path ~/Work/blog --php 8.2 --title "Client Blog" \
  --admin-user andrea --admin-email me@example.com
benbow wordpress blog --domain blog.benbow.test --group Clients/Acme --ini memory_limit=1G
```

In one step Benbow:

1. creates the folder (it must be new or empty);
2. copies WordPress 7.1.4, pinned by SHA-256 in the manifest (the archive's
   SHA-1 was checked against wordpress.org's when it was pinned);
3. adds the site (`https://blog.localhost:8443`);
4. creates the database `blog` with its own MySQL user (see `mysql.md`);
5. writes `wp-config.php` and runs the installation with wp-cli.

The admin password is generated (24 characters), shown once and not kept.
Passwords reach wp-cli on its standard input, never as arguments.
If any step fails, everything created so far is removed again; a folder
that already existed is emptied, not deleted. MySQL must be on and Benbow
running. `--domain`, `--aliases`, `--group` and `--ini` work as for
`benbow site add`. In the app: + → New WordPress Site… (or New Site… →
WordPress).

## wp-cli

```sh
benbow wp blog plugin list
benbow wp blog user update admin --prompt=user_pass
benbow wp blog -- search-replace old.example.com blog.localhost:8443
```

`benbow wp <site>` runs wp-cli 2.12.0 (pinned by SHA-256, checked against the
published SHA-512) with the command-line PHP matching the site's version
exactly (static-php-cli "cli" builds, pinned like PHP-FPM), Benbow's php.ini
(MySQL socket and port, CA bundle; no Xdebug), `memory_limit=-1` and errors
on stderr. Arguments, output and exit code are passed through unchanged:
there is no `--json` envelope. wp-cli's cache and packages live in
`data/wp-cli` inside the Benbow folder, not in `~/.wp-cli`.

API: `POST /v1/wordpress` with `name`, `path`, `php`, `title`, `admin_user`,
`admin_email`, `domain`, `aliases`, `group_id`, `php_settings`.

New sites can start from a blueprint (themes, plugins, options): `benbow wordpress <name> --blueprint <blueprint>`, see `blueprints.md`.
