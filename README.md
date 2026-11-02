# Covo

A native macOS local development environment for people who build PHP and
WordPress sites: an open source, modern alternative to MAMP PRO.

Covo never breaks your Mac. It does not use or modify Homebrew, installs
nothing system-wide, and coexists with MAMP, Valet, Herd or anything else
already running. Everything lives in `~/Library/Application Support/covo/`,
and `covo uninstall` puts your Mac back the way it was.

**Status:** early development, not ready for use.

## Build

Requires Go (from go.dev) on macOS.

```sh
go build -o covo ./cmd/covo
./covo site add demo --php 8.4 --path ~/Sites/demo
./covo start
./covo trust          # one macOS confirmation, no sudo
open https://demo.localhost:8443
./covo site set demo --php 8.2   # also --path, --domain; applied at once
```

Every command supports `--json`.

## Development

```sh
go test ./...
go test -tags integration ./test/e2e/   # real downloads, launchd and HTTPS, isolated from real installs
go tool golangci-lint run
```

## License

Copyright (C) 2026 Andrea Casali.

Covo is free software: you can redistribute it and/or modify it under the
terms of the GNU General Public License, version 3 only
(`SPDX-License-Identifier: GPL-3.0-only`). See [LICENSE](LICENSE).

Covo is distributed in the hope that it will be useful, but WITHOUT ANY
WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
A PARTICULAR PURPOSE.

The Covo name and logo are not covered by the license: see
[TRADEMARKS.md](TRADEMARKS.md). Contributions: see
[CONTRIBUTING.md](CONTRIBUTING.md).
