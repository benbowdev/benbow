CREATE TABLE site_migrations (
    site TEXT PRIMARY KEY REFERENCES sites(name) ON DELETE CASCADE,
    source TEXT NOT NULL,
    db_name TEXT NOT NULL DEFAULT '',
    db_user TEXT NOT NULL DEFAULT '',
    created_db INTEGER NOT NULL DEFAULT 0,
    config_path TEXT NOT NULL DEFAULT '',
    backup_path TEXT NOT NULL DEFAULT '',
    url_from TEXT NOT NULL DEFAULT '',
    url_to TEXT NOT NULL DEFAULT '',
    at TEXT NOT NULL
);
