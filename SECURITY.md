# Security policy

Covo runs on your Mac with your user's permissions, creates a local
certificate authority and, if you opt in, a small helper for `/etc/hosts` and
a resolver for `.test`. We take reports about any of these seriously.

## Reporting a vulnerability

Please **do not open a public issue** for security problems.

Report privately by either:

- email to **security@getcovo.dev**, or
- GitHub's private vulnerability reporting ("Report a vulnerability" in the
  Security tab of `getcovo/covo`).

Include what you found, how to reproduce it, the Covo version
(`covo version`) and your macOS version. If you are not sure whether
something is a vulnerability, report it anyway.

## What to expect

- An acknowledgement within 3 working days.
- An assessment and, if confirmed, a plan for a fix within 14 days.
- Credit in the release notes once a fix ships, unless you prefer to stay
  anonymous.

Covo is maintained by one person, so please allow some flexibility. Please
give us a reasonable time to ship a fix before disclosing publicly; we will
agree on a date with you.

## Supported versions

Covo is in early development. Only the latest release receives security
fixes.

## Scope

In scope: the `covo` CLI and core, the macOS app, the `.local` hosts helper,
the Covo certificate authority and its name constraints, the binaries Covo
downloads and verifies (including those built in `getcovo/covo-binaries`),
and the local API socket.

Vulnerabilities in upstream components (PHP, Caddy, MySQL, Mailpit, Xdebug,
Adminer) should be reported upstream; tell us too if Covo ships an affected
version.
