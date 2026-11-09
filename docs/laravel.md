# Laravel

## Existing projects

Add the project folder (or its `public` folder) like any site: Covo
recognises Laravel (`artisan` plus `laravel/framework` in `composer.json`),
serves `public/` and runs tools in the project folder. The site detail shows
"Laravel" and the project folder. WordPress is recognised the same way
(`wp-includes/version.php`). Sites added before are recognised when Covo
next opens.

## New project

```sh
covo laravel shop                        # ~/Sites/shop, PHP 8.4
covo laravel shop --database sqlite --path ~/Work/shop --php 8.3
```

Covo runs `composer create-project laravel/laravel`, adds the site and sets
in `.env`: `APP_NAME`, `APP_URL` (the site's address), the database — a new
MySQL database with its own user when MySQL is running (`DB_HOST=127.0.0.1`
and Covo's port), else Laravel's SQLite file — and Mailpit's SMTP port when
mail is on. With a database it runs `artisan migrate`. If any step fails,
everything created is removed. Laravel needs PHP 8.2 or newer. No queue
worker or scheduler is started. In the app: + → New Site… → Laravel.

## Vite (npm run dev)

Give the site a companion and Covo starts Vite with it, over HTTPS on the
site's own domain:

```sh
covo site set shop --companion "npm run dev -- --port {port} --strictPort"
```

See "A companion next to a PHP site" in `apps.md`.

## Composer and Artisan

```sh
covo composer shop require guzzlehttp/guzzle
covo artisan shop migrate
covo artisan shop make:model Post -m
```

Both run with the site's exact PHP version and Covo's php.ini (also for
PHP that Composer scripts start, through `PHPRC`), never reading `.ini`
files from elsewhere. Arguments, output and exit code pass through
unchanged. Composer 2.11.1 is pinned by SHA-256 (getcomposer.org's
published checksum); sites on PHP older than 7.3 get Composer 2.2.31 LTS.
Composer's home and cache live in `data/composer` inside the Covo folder,
not in `~/.composer`. In the app: the site's Artisan and Composer tool.

API: `POST /v1/laravel` with `name`, `path`, `php`, `database` (`new` or
`sqlite`), `domain`, `aliases`, `group_id`, `php_settings`;
`POST /v1/sites/{name}/run` with `tool` (`wp`, `composer`, `artisan`) and
`args`, returning `exit_code`, `output`, `errors` (256 KiB each).
