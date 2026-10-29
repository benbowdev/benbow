# Xdebug

```
covo php xdebug           # state for each installed PHP version
covo php xdebug on 8.4    # turn it on (restarts PHP 8.4 if Covo is running)
covo php xdebug off 8.4
```

When on, `php.ini` loads `xdebug.so` by absolute path with:

| Setting | Value | Why |
|---|---|---|
| `xdebug.mode` | `debug` | step debugging only: no profiler, no overloaded `var_dump`, no tracing |
| `xdebug.start_with_request` | `trigger` | no cost unless the browser extension, an `XDEBUG_TRIGGER` cookie or parameter asks |
| `xdebug.client_host` / `client_port` | `127.0.0.1` / `9003` | the IDE listens locally; Covo binds no port for Xdebug |

## Where xdebug.so comes from

The static-php-cli builds Covo runs do not include Xdebug and ship no headers
to build it against. Covo's `xdebug.so` files
are built in public by
[getcovo/covo-binaries](https://github.com/getcovo/covo-binaries)
(`xdebug/build.sh`, GitHub Actions, macOS arm64):

- PHP source (php.net), Xdebug source (xdebug.org, same hash as PECL) and the
  exact static-php-cli PHP-FPM archive Covo pins, each checked against a
  pinned SHA-256;
- a minimal PHP of the same version (NTS, non-debug) provides `phpize` and the
  headers; `xdebug.so` targets macOS 12.0 like the static-php-cli builds;
- every symbol `xdebug.so` needs must be exported by that PHP-FPM binary or
  by the system (checked with `nm` and `dlsym`: macOS binds lazily, so a
  missing one would otherwise fail only when first called), and the binary
  must load it and report the expected Xdebug version.

Each `xdebug.so` is pinned in Covo's manifest by SHA-256 and tied to one exact
PHP version (`"for": "8.4.23"`): Covo never loads a build made for another
PHP release. Updating PHP means building and pinning a matching `xdebug.so`.
Apple Silicon only for now.
