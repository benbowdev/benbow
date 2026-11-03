CREATE TABLE site_remotes (
    site TEXT NOT NULL REFERENCES sites(name) ON DELETE CASCADE,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    transport TEXT NOT NULL,
    host TEXT NOT NULL,
    port INTEGER NOT NULL DEFAULT 0,
    user TEXT NOT NULL DEFAULT '',
    path TEXT NOT NULL DEFAULT '',
    url TEXT NOT NULL DEFAULT '',
    webroot TEXT NOT NULL DEFAULT '',
    paths TEXT NOT NULL DEFAULT '[]',
    excludes TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (site, name)
);
