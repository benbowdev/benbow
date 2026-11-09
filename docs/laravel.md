# Laravel

## Existing projects

Add the project folder (or its `public` folder) like any site: Benbow
recognises Laravel (`artisan` plus `laravel/framework` in `composer.json`),
serves `public/` and runs tools in the project folder. The site detail shows
"Laravel" and the project folder. WordPress is recognised the same way
(`wp-includes/version.php`). Sites added before are recognised when Benbow
next opens.

## New project

```sh
benbow laravel shop                        # ~/Sites/shop, PHP 8.4
benbow laravel shop --database sqlite --path ~/Work/shop --php 8.3
```

Benbow runs `composer create-project laravel/laravel`, adds the site and sets
in `.env`: `APP_NAME`, `APP_URL` (the site's address), the database — a new
MySQL database with its own user when MySQL is running (`DB_HOST=127.0.0.1`
and Benbow's port), else Laravel's SQLite file — and Mailpit's SMTP port when
mail is on. With a database it runs `artisan migrate`. If any step fails,
everything created is removed. Laravel needs PHP 8.2 or newer. No queue
worker or scheduler is started. In the app: + → New Site… → Laravel.

## Vite (npm run dev)

Give the site a companion and Benbow starts Vite with it, over HTTPS on the
site's own domain:

```sh
benbow site set shop --companion "npm run dev -- --port {port} --strictPort"
```

See "A companion next to a PHP site" in `apps.md`.

## Composer and Artisan

```sh
benbow composer shop require guzzlehttp/guzzle
benbow artisan shop migrate
benbow artisan shop make:model Post -m
```

Both run with the site's exact PHP version and Benbow's php.ini (also for
PHP that Composer scripts start, through `PHPRC`), never reading `.ini`
files from elsewhere. Arguments, output and exit code pass through
unchanged. Composer 2.11.1 is pinned by SHA-256 (getcomposer.org's
published checksum); sites on PHP older than 7.3 get Composer 2.2.31 LTS.
Composer's home and cache live in `data/composer` inside the Benbow folder,
not in `~/.composer`. In the app: the site's Artisan and Composer tool.

API: `POST /v1/laravel` with `name`, `path`, `php`, `database` (`new` or
`sqlite`), `domain`, `aliases`, `group_id`, `php_settings`;
`POST /v1/sites/{name}/run` with `tool` (`wp`, `composer`, `artisan`) and
`args`, returning `exit_code`, `output`, `errors` (256 KiB each).
