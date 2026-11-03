CREATE TABLE snapshots (
    id TEXT NOT NULL,
    site TEXT NOT NULL REFERENCES sites(name) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    kind TEXT NOT NULL,
    note TEXT NOT NULL DEFAULT '',
    db_engine TEXT NOT NULL DEFAULT '',
    db_name TEXT NOT NULL DEFAULT '',
    db_file TEXT NOT NULL DEFAULT '',
    files_dir TEXT NOT NULL DEFAULT '',
    root TEXT NOT NULL DEFAULT '',
    bytes INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (site, id)
);
