# Sites

A site is a folder served at `https://<name>.localhost:8443` (or another
domain under `.localhost`, `.test` or `.local`, see `domains.md`) with its own
PHP version.

```sh
covo site add shop --php 8.4 --path ~/Sites/shop   # --domain shop.test
covo site list
covo site set shop --php 8.2                       # also --path, --domain
covo site disable shop                             # keep it, stop serving it
covo site enable shop
covo site remove shop                              # the folder is never touched
```

Changes apply at once when Covo is running; if applying fails, the previous
settings are restored. A folder without `index.php` or `index.html` needs
`--force` (the app asks).

A disabled site stays in the list but Caddy does not serve it, and a PHP
version used only by disabled sites is not started.

API: `GET/POST /v1/sites`, `PATCH /v1/sites/{name}` with any of
`php`, `path`, `domain`, `enabled`, `force`, and `DELETE /v1/sites/{name}`.
