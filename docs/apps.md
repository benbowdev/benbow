# Apps (dev servers in any language)

An app is a site whose pages come from a dev server instead of PHP: Next.js,
Vite, Astro, Nuxt, SvelteKit, Django, FastAPI, Flask, Rails or anything
else that listens on a port. Covo gives it a domain with trusted HTTPS
(`https://<name>.localhost:8443`), aliases, a group, a database, logs and
on/off, like a PHP site.

Covo does not manage runtimes. It never installs or picks a version of Node,
Python, Ruby or their packages: the command runs with the tools you already
have (nvm, fnm, mise, volta, asdf, uv, pyenv, or the system's). Covo stays
PHP-first; apps are for the dev server next to your PHP work.

```sh
covo site detect ~/Code/shop                       # proposes the command
covo site add shop --path ~/Code/shop --command "npm run dev -- --port {port} --strictPort"
covo site add api --path ~/Code/api --proxy-port 3000   # you run the server yourself
covo site set shop --command "pnpm dev"            # also --port, --proxy-port
covo site set api --host-header localhost          # "" for the site's domain
```

## Two modes

- **Covo starts the command** (`--command`). Covo gives the app a free port
  from 41000 (or `--port`), away from the ports dev servers use by default,
  so a server you start by hand never collides with it. The port is set as
  `PORT` and replaces `{port}` in the command, for tools that ignore `PORT`
  (Vite, Astro, Nuxt…). It stays the same for the site.
- **Covo only proxies** (`--proxy-port`): you run the dev server in your
  terminal on that port, Covo puts the domain and HTTPS in front of it.

A started app's port must be free when it is chosen; a proxied app's port
is normally taken (by your server). Two Covo sites never share a port.

## Detecting the command

`covo site detect [folder]` (and New Site in the app) reads the project and
proposes a command. You always confirm or edit it.

| Project | Proposed command |
|---|---|
| Next.js, Create React App (`next`, `react-scripts`) | `npm run dev` (they read `PORT`) |
| Vite, SvelteKit, Remix / React Router (`vite`…) | `npm run dev -- --port {port} --strictPort` |
| Astro, Nuxt | `npm run dev -- --port {port}` |
| other `package.json` | its `dev`, `start` or `serve` script |
| Django (`manage.py`) | `python manage.py runserver 127.0.0.1:{port}` |
| FastAPI | `uvicorn <module>:<app> --reload --port {port}` |
| Flask | `flask --app app run --debug --port {port}` |
| Rails (`bin/rails`) | `bin/rails server -p {port}` |

The package manager comes from `packageManager` in package.json, else the
lockfile (pnpm, yarn, bun, npm). npm needs `--` before the script's own
options; the others pass them on. Python commands use the project's
`.venv` or `venv` when there is one, else `uv run` (uv.lock) or `poetry run`
(poetry.lock), else `python` from your PATH.

## The proxy

Caddy proxies the domain and its aliases to `localhost:<port>`, trying both
127.0.0.1 and ::1 (Node often listens on ::1 only). WebSockets (hot reload)
pass through. The app receives `X-Forwarded-Proto: https`,
`X-Forwarded-Host` and `X-Forwarded-For`.

By default the app receives the site's domain as `Host`, so links and
redirects it builds are right. `--host-header localhost` sends
`Host: localhost:<port>` instead, for servers that only accept localhost.

## Limits

- PHP tools (wp-cli, Composer, Artisan, php.ini, the PHP compatibility
  check) refuse apps. A PHP site cannot become an app in place: remove it
  (its folder is never touched) and add it again with `--command`.
- Open on Phone does not support apps yet.

API: `POST /v1/sites` takes `command`, `port`, `proxy_port`, `app_host`;
`PATCH /v1/sites/{name}` takes `command`, `port`, `proxy_port`, `app_host`;
`GET /v1/detect?path=` returns `{path, framework, command, package_manager,
notes}`. Sites carry `kind` (`php` or `app`), `app_mode` (`run` or `proxy`),
`command`, `app_port` and `app_host`. MCP: `detect_app`, and `add_site` /
`update_site` with `command`, `port`, `proxy_port`.
