# Local CA

Covo serves its sites over HTTPS with certificates from its own local
certificate authority. Trusting a CA is powerful: a trusted root can vouch for
any website. Covo's CA is built so that it can only ever vouch for local
development names.

## Design

- **Created by Covo** (`internal/ca`), not by Caddy. ECDSA P-256, 10 years.
- **Name Constraints** on the root and on the intermediate:
  - permitted DNS names: `localhost`, `test`, `local` and their subdomains;
  - every IP address excluded (`0.0.0.0/0`, `::/0`).

  RFC 5280 lets a verifier ignore constraints on a trust anchor. Constraints on
  an intermediate are always enforced, so they are on both.
- **No root key on disk.** The root private key exists in memory only while it
  signs the intermediate, then it is discarded. Nobody, Covo included, can
  ever mint another intermediate under the trusted root.
- **Caddy signs site certificates with the intermediate**: it gets the root
  certificate (no key) and the intermediate with its key, in
  `data/ca/` (0700, files 0600). Caddy does not create its own PKI.
- **Renewal.** `start` regenerates the CA if it is missing, unconstrained or
  within 30 days of expiry. A new root must be trusted again
  (`covo trust`, one macOS dialog).
- **Migration from the spike.** The spike used Caddy's unconstrained CA
  (`data/caddy/pki`). `covo trust` removes it from the keychain (by SHA-1) and
  deletes it; `untrust` and `uninstall` handle both CAs.

## PHP

PHP does not read the macOS keychain: its OpenSSL uses a CA file. Covo writes
`etc/ca-bundle.pem` = Mozilla's roots (pinned `cacert-YYYY-MM-DD.pem` from
curl.se, SHA-256 checked against the published one) + Covo's root, and sets
`openssl.cafile` and `curl.cainfo` in php.ini. A site can then call itself
over HTTPS (WordPress loopback requests, cron, REST) with verification on. The
bundle is rewritten on every start; a change restarts PHP-FPM.

## Verification

Automated (`go test ./internal/ca/`, `go test -tags integration ./test/e2e/`):

- certificates signed by the intermediate for `demo.localhost`,
  `demo.covo.test` and `studio.local` verify; `example.com`,
  `localhost.example.com`, `test.example.com`, `demo.covo`, `127.0.0.1` and
  `::1` are rejected (Go `crypto/x509`);
- the chain Caddy serves is leaf + Covo's constrained intermediate; macOS's
  verifier (`security verify-cert` with Covo's root as the only anchor, no
  keychain change) accepts it and rejects `example.com`,
  `localhost.example.com` and `127.0.0.1` signed by the same intermediate;
- PHP-FPM fetches `https://demo.localhost:<port>/` with `file_get_contents`
  and curl, verification on; with Mozilla's roots alone the same request fails
  (`unable to get local issuer certificate`).

Browsers: see below.
