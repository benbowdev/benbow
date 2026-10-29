CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE php_versions (
    version      TEXT PRIMARY KEY,
    minor        TEXT NOT NULL,
    path         TEXT NOT NULL,
    sha256       TEXT NOT NULL,
    installed_at TEXT NOT NULL
);

CREATE TABLE sites (
    name       TEXT PRIMARY KEY,
    path       TEXT NOT NULL,
    php_minor  TEXT NOT NULL,
    domain     TEXT NOT NULL UNIQUE,
    https      INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);
