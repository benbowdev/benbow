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
covo site restart shop                             # stop every process, start again
covo site logs shop --app                          # what the command printed
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

## How Covo runs the command

Each started app is a launchd agent (`dev.getcovo.app-<name>`) whose program
is Covo itself (`covo app run <name>`, hidden). It runs the command the way
a new Terminal window would:

```sh
$SHELL -l -i -c 'cd "$COVO_APP_DIR" || exit 1
<your command>'
```

- `$SHELL` is your login shell (from your user record: launchd sets none).
  `-l -i` reads `.zprofile` and `.zshrc` (or bash's and fish's files), so
  nvm, pyenv, rbenv, asdf, volta, Homebrew's PATH and the like work as in
  your terminal. The `cd` happens after your profile, so tools that switch
  versions on entering a folder (fnm `--use-on-cd`, mise, direnv) see the
  project's `.nvmrc`, `.node-version`, `.tool-versions` or `.envrc`.
- The environment adds `PORT`, `COVO_APP_DIR` (the folder),
  `NODE_EXTRA_CA_CERTS` (Covo's CA, so Node trusts `https://*.localhost`
  sites), `SSL_CERT_FILE` and `REQUESTS_CA_BUNDLE` (Mozilla's roots plus
  Covo's, for Python), `NO_COLOR=1`, `TERM=dumb`, `BROWSER=none` (no browser
  tab opened on start), `PYTHONUNBUFFERED=1` and `COVO_APP_RUN` (a marker
  Covo uses to find the command's processes). Covo's own variables are
  removed.
- stdin is empty: a command that asks a question gets no answer.
- The output goes to `logs/sites/<name>/app.log` (`covo site logs <name>
  --app`, the Logs window), copied to `app.log.1` and emptied past 10 MB.

Before starting, Covo checks the folder exists and the port is still free.
The address the server listens on is the server's choice, as in your
terminal: most listen on localhost only, some (Node's `listen(port)`) on
every interface.

### Stopping

Stopping the app (Stop, turning the site off, removing it, changing its
command, port or folder, `covo site restart`) stops every process the
command started: its process group, the processes started under it (seen
every 2 seconds) and any process carrying its `COVO_APP_RUN` marker, which
finds workers that left the group with `setsid`. They get SIGTERM, then
SIGKILL after 5 seconds.

### No automatic restart

launchd never restarts an app. When the command ends, the processes it left
behind are stopped and the site shows the state below until you restart it
(`covo site restart`, Restart in the app) or press Start, which starts every
app that has ended. Other changes (a new site, a PHP setting) do not start
an app that has ended.

### State

`covo status`, `covo site list` and the API give each app an `app` object:

| `state` | Meaning |
|---|---|
| `starting` | the command runs, its port does not answer yet (up to 90 s) |
| `running` | the port answers |
| `error` | the command ended with an error, or ran 90 s without opening its port |
| `exited` | the command ended with status 0 (a dev server should not) |
| `stopped` | Covo or the site is off |
| `waiting` | a proxied app: nothing listens on its port |

With `error` and `exited` come `exit_code`, `message` and `log_tail` (the
last 20 lines printed since the start). The message names a missing program
(`node was not found by your login shell. Install it with your version
manager (nvm, fnm, mise, volta…)`), a port in use, or a signal. Covo never
installs runtimes or packages.

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

API: `POST /v1/sites/{name}/restart` restarts an app and returns its state;
`GET /v1/sites/{name}/logs/app` is its output.
`POST /v1/sites` takes `command`, `port`, `proxy_port`, `app_host`;
`PATCH /v1/sites/{name}` takes `command`, `port`, `proxy_port`, `app_host`;
`GET /v1/detect?path=` returns `{path, framework, command, package_manager,
notes}`. Sites carry `kind` (`php` or `app`), `app_mode` (`run` or `proxy`),
`command`, `app_port`, `app_host` and, in status, `app` (the state above).
MCP: `detect_app`, `restart_app`, `site_logs` with kind `app` (the default
for apps), and `add_site` / `update_site` with `command`, `port`,
`proxy_port`.
