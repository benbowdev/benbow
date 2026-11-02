# Importing from MAMP PRO

```
covo import mamp --dry-run     # what would be created; changes nothing
covo import mamp               # keep .local names (needs `covo domains enable local`)
covo import mamp --localhost   # serve them as <name>.localhost instead
```

MAMP PRO is never modified and can keep running next to Covo. Covo copies
`~/Library/Application Support/appsolute/MAMP PRO/settings6.plist` into its own
`tmp/` folder, converts the copy with `/usr/bin/plutil` and deletes it: the
original is only read once, by the copy.

## Mapping

| MAMP PRO | Covo |
|---|---|
| `shop.local` | site `shop`, domain `shop.local` (or `shop.localhost` with `--localhost`) |
| `blog.client.local` | site `blog-client`, domain `blog.client.local` |
| a name without suffix (`intranet`) | `intranet.local`, with a warning: Covo only serves reserved suffixes |
| `localhost` (MAMP's default host) | skipped |
| document root | the same folder, used in place: nothing is copied or moved |
| PHP 8.x | the same minor version (8.1.13 → 8.1, 8.2.0 → 8.2) |
| PHP 7.4 and older | PHP 8.1, with a warning: Covo has no PHP 7 builds |
| SSL | always on: every Covo site is HTTPS |
| aliases | imported, mapped like the server name; one already used by another site is skipped with a warning |
| databases | not imported: listed, to export from MAMP's MySQL (port 3306) and import into Covo's (3307) |

Hosts are skipped when their folder does not exist, when Covo refuses the
folder (your home, a system folder, Covo's own), when the name is not a valid
host name, or when a Covo site already uses the domain. A second import only
adds what is missing.

All sites of one import are created together: if one fails, none is kept.

PHP 8.1 is end of life upstream; sites moved to it from 7.4 should be
tested, and moved to 8.2 or later when possible.
