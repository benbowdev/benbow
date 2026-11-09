# Ports

Site addresses use the HTTPS port: `https://shop.localhost:8443` by default,
or `https://shop.localhost` with the standard port 443. An optional HTTP port
redirects `http://` addresses to HTTPS. Like in MAMP, both can be changed:

```sh
benbow ports                          # show
benbow ports --https 443              # no port in addresses
benbow ports --http 80                # http:// redirects to https:// (0: off)
benbow ports --https 9443 --http 0    # back to a custom port
```

| Port | How |
|---|---|
| 1024–65535 | Caddy listens on it directly, on 127.0.0.1 and ::1 |
| 443, 80 | Caddy listens on a high port (from 8443, from 8080); Benbow's helper forwards the standard port to it |

Benbow never runs as root, so it cannot listen on 443 or 80 itself. For those
ports it installs its helper (the same one as for `.local`, see
`domains.md`), with one administrator password. The helper loads two pf
rules per port into Benbow's own anchor, `com.apple/dev.benbow.hostshelper`,
inside the anchor point macOS's `/etc/pf.conf` already has (`pf.conf` is
never changed):

```
rdr pass on lo0 inet proto tcp from any to 127.0.0.1 port 443 -> 127.0.0.1 port 8443
rdr pass on lo0 inet6 proto tcp from any to ::1 port 443 -> ::1 port 8443
```

Only loopback connections are forwarded: nothing is reachable from the
network. The helper builds the rules from port numbers only (from below
1024 to 1024–65535, at most two) and keeps pf enabled with its own
reference (`pfctl -E` / `pfctl -X`), so other pf users are unaffected.

The forwards exist only while Benbow is running: `benbow start` puts them in
place, `benbow stop`, `benbow uninstall` and the helper stopping remove them.
Before forwarding, Benbow checks nobody listens on the standard port, on any
address: a port another program uses (MAMP on 80, for example) is refused
and left to it. Setting a high port again removes the forwards, and the
helper too unless `.local` domains still need it.

WordPress stores its address in its database. After changing the HTTPS port,
update existing WordPress sites, e.g.
`benbow wp shop search-replace https://shop.localhost:8443 https://shop.localhost`.

In the app: Settings → General → Ports.

API: `GET /v1/ports`, `PUT /v1/ports` with `https_port`, `http_port`
(0: off).
