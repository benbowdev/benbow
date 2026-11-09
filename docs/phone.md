# Open on phone

```sh
benbow phone shop                 # open shop on the local network for 30 minutes
benbow phone shop --minutes 120
benbow phone                      # what is shared, and where
benbow phone --off
```

One site at a time is served, besides its usual address, at
`https://<site>-<mac>.local:<port>` (port from 9443) on the Mac's private
network addresses only (192.168.x.x, 10.x.x.x, 172.16–31.x.x of interfaces
that are up; never loopback or public addresses). The name is published with
Bonjour by `/usr/bin/dns-sd -P` (a launchd agent while sharing). It turns
itself off after the chosen time (default 30 minutes, at most 24 hours): the
daemon checks every 30 seconds. The app shows an indicator in the toolbar and
the menu bar while a site is shared.

While shared, the network gets 404 for dotfiles and folders (`.git`, `.env`;
`/.well-known/` stays), dumps and archives (`*.sql`, `*.sql.gz`, `*.zip`,
`*.tar.gz`), `*.bak` and `*.log` files, before PHP or the file server see the
request.

## Quick address: plain HTTP, nothing to install

The QR code the app shows first opens `http://<Mac's address>:<port>` (the
port after the CA page's), the same site over plain HTTP on the same private
addresses: no certificate on the phone, no `.local` name to resolve (works
on Android too). Features that need a secure context (camera, location,
service workers, payments) or a site that forces https need the HTTPS
address below: Use HTTPS Instead… in the app. The status has `plain_url`.

## HTTPS without weakening the CA

The certificate comes from Benbow's CA, whose Name Constraints already allow
`.local` names: nothing about the CA changes. A plain-HTTP page on the same
addresses (port after the site's) installs it on the phone:
`benbow-ca.mobileconfig` for iOS (then Settings → General → About →
Certificate Trust Settings) and `benbow-ca.crt` for Android, with the
fingerprint to compare and why it is safe: the CA can only vouch for
`.localhost`, `.test` and `.local`. The app shows a QR code for that page and
one for the site.

## Addresses without touching the site

The site's database and files are not changed. Only for the phone's requests
Caddy sends `BENBOW_PHONE_URL` and `BENBOW_LOCAL_URL` and PHP prepends Benbow's
`data/phone/prepend.php`, which:

- defines `WP_HOME` and `WP_SITEURL` as the phone address (unless the site's
  `wp-config.php` already defines them), so WordPress does not redirect to
  the local address;
- replaces the local address (also JSON-escaped) with the phone address in
  the page.

Laravel gets `APP_URL` from the environment (its `.env` does not override an
existing variable). Redirects to the local address are rewritten by Caddy.

## Limits

- The phone must be on the same network. iOS and macOS resolve `.local` names;
  Android often does not.
- If the macOS firewall is on, macOS may ask whether `caddy` may accept
  incoming connections; with "Block all incoming connections" nothing gets
  through. Benbow reads the firewall state (`socketfilterfw --getglobalstate`)
  and says so; it never changes it.
- WordPress installed in a subfolder (siteurl ≠ home) is shown with
  `WP_SITEURL` equal to the phone home address.

## Apps

An app (`apps.md`) opens on the phone too: Caddy proxies the phone address
to its dev server. There is no PHP to rewrite pages, so only redirects get
the phone address; absolute URLs the server writes in its pages (built from
`APP_URL`, for example) keep the local one. The dev server sees the phone
address as `Host` (or `localhost:<port>` with `--host-header localhost`),
and most refuse a host they do not know: allow it in Vite
(`server.allowedHosts: ['.local']`), Next.js (`allowedDevOrigins:
['*.local']`) and Django (`ALLOWED_HOSTS`), or use `--host-header
localhost`. Hot reload goes through the same address. Dotfiles stay hidden
as for PHP sites, except Vite's `/node_modules/.vite/` dependencies.

API: `GET /v1/phone`, `PUT /v1/sites/{name}/phone` (`minutes`),
`DELETE /v1/phone`. In the app: the site's Open on Phone tool.
