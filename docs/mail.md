# Mail

```sh
covo mail enable     # catch every mail PHP sends
covo mail            # status: inbox URL, SMTP port
covo mail disable    # stop catching (messages are kept)
```

Covo runs Mailpit 1.33.0 (one static binary from the official GitHub
release, pinned by SHA-256, the same as GitHub's published digest) as the
launchd agent `dev.getcovo.mailpit`:

| | |
|---|---|
| Inbox | `https://mail.localhost:8443/` (Caddy proxies Mailpit's web UI) |
| SMTP | `127.0.0.1:1025`, or the first free port after it |
| Web UI | `127.0.0.1:8025` (or the next free port), loopback only |
| Data | `data/mailpit/mailpit.db`, at most 5000 messages |

While mail is on, `php.ini` sets `sendmail_path` to Mailpit's `sendmail`
mode, so PHP's `mail()` (and WordPress's `wp_mail()`) deliver to the local
inbox and nothing leaves the Mac. Code that speaks SMTP can use the SMTP
address directly. `mail.localhost` cannot be used by a site.

In the app: Settings → Mail, and the envelope button in the toolbar.
API: `GET /v1/mail`, `POST /v1/mail/enable`, `POST /v1/mail/disable`.
