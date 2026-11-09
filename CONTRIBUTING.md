# Contributing to Benbow

Thank you for your interest in Benbow. Everyone taking part is expected to
follow the [Code of Conduct](CODE_OF_CONDUCT.md). To report a security
problem, see [SECURITY.md](SECURITY.md) instead of opening an issue.

## External contributions

External code contributions (pull requests) are **not accepted yet**. They
will be accepted once a Contributor License Agreement (CLA) is in place; this
file will be updated with how to sign it.

Until then you are welcome to:

- open issues to report bugs or suggest features;
- discuss design and ideas in issues.

Please do not open pull requests with code before the CLA is active: they
will be closed without review.

## Conventions

For when contributions open:

- Go code lives in `cmd/` and `internal/`; tests are table-driven, errors are
  wrapped.
- Every Go file starts with the license header:

  ```go
  // SPDX-License-Identifier: GPL-3.0-only
  // Copyright (C) 2026 Andrea Casali
  ```

- Small commits following Conventional Commits.
- Before submitting: `go test ./...` and `go tool golangci-lint run`.
