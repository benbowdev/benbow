# Public link

```sh
benbow tunnel shop                  # shop on the internet for 60 minutes
benbow tunnel shop --minutes 120    # same site: more time, same address
benbow tunnel shop --password       # visitors need a password
benbow tunnel                       # the link, and how long it has left
benbow tunnel --off
```

One site at a time gets an address on the internet,
`https://<random words>.trycloudflare.com`, through a Cloudflare quick
tunnel: no Cloudflare account and nothing to configure. It is for payment
providers and other services that send webhooks to your site, and for
showing work to a client. In the app: the Public Link tile in the site's
tools; the toolbar and the menu bar show the link while it is open.

The link closes after the chosen time (default 60 minutes, at most 8 hours;
the daemon checks every 30 seconds) and whenever Benbow stops: it never
comes back by itself. Each new link has a new address (quick tunnels cannot
keep one); asking again for the same site while its link is open only
changes the time and keeps the address and the password.

## Benbow's own cloudflared

Benbow downloads Cloudflare's `cloudflared` the first time a link is opened,
pinned in its manifest with its SHA-256 like every other component, into
`bin/cloudflared/<version>/`, and updates it with `benbow update` (the
previous version stays for `rollback`). A `cloudflared` the user installed
is never used, and nothing outside Benbow's folder is touched: it runs as
the launchd agent `dev.benbow.tunnel` with

- `etc/cloudflared.yml`, written by Benbow: `no-autoupdate` and one ingress
  rule to Caddy's socket (a quick tunnel still wants `--url` on the command
  line; the rule wins);
- `HOME` set to `data/tunnel/home`, so `~/.cloudflared` and the user's
  `config.yml` are not read;
- its local metrics server on `127.0.0.1` (from port 20241), where Benbow
  reads the address (`/quicktunnel`) and whether it is connected (`/ready`);
- its log in `logs/tunnel.log`.

## What the internet sees

cloudflared talks to Caddy over the Unix socket `run/tunnel.sock` (0600),
never a TCP port, and Caddy serves the site there:

- 404 for dotfiles and folders (`.git`, `.env`; `/.well-known/` stays),
  dumps and archives (`*.sql`, `*.sql.gz`, `*.zip`, `*.tar.gz`), `*.bak`
  and `*.log`, as for Open on phone;
- `X-Robots-Tag: noindex, nofollow`;
- with `--password`, HTTP basic authentication, user `benbow` and a
  generated password shown once (only its bcrypt hash is stored; to get a
  new one, close the link and open another). Webhooks cannot send a
  password: leave it off for them;
- Adminer and Mail are other hosts and are never reachable.

## Addresses without touching the site

As for Open on phone (`phone.md`), the site's files and database are not
changed. Caddy passes the public name cloudflared sends as `Host`, and for
these requests only:

- PHP gets `HTTPS=on`, `BENBOW_PHONE_URL` (the public address) and
  `BENBOW_LOCAL_URL`, and Benbow's `data/phone/prepend.php` defines
  `WP_HOME`/`WP_SITEURL` as the public address (unless `wp-config.php`
  defines them) and replaces the local address in pages;
- Laravel gets `APP_URL`;
- redirects to the local address are rewritten.

Apps (`apps.md`) go to their dev server; only redirects are rewritten, and
the dev server must accept the public name (Vite: `server.allowedHosts`).

## Ready means reachable

A new quick tunnel name takes a few seconds to appear in DNS. The link is
`ready` once cloudflared is connected and Cloudflare's public resolver
(1.1.1.1) knows the name; until then the app shows "waiting" and the CLI
prints the address with a note. Benbow does not ask the Mac's own resolver
first: a name looked up too early would be remembered as missing for a
while and the link would not open.

## Limits

- The site is served by your Mac: only while it is awake and Benbow runs,
  at the speed of your upload.
- Quick tunnels are meant for testing: Cloudflare gives no uptime
  guarantee and limits concurrent requests. A webhook's URL must be pasted
  again in the provider's dashboard for every new link.
- Not built yet: a named tunnel with a fixed address (needs a Cloudflare
  account and a domain), several links at once, a link limited to one path.

## API

`GET /v1/tunnel`, `PUT /v1/sites/{name}/tunnel` `{minutes, password}`,
`DELETE /v1/tunnel`. The status (`benbow status`, `GET /v1/status`) has
`tunnel` while a link is open: `site`, `url` ("" until Cloudflare gave one),
`ready`, `minutes_left`, `protected`, `local_url`; `user` and `password`
only in the answer that created the password. There is no MCP tool to open
a link: an agent must not be able to put a site on the internet.
