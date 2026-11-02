CREATE TABLE site_aliases (
    domain TEXT PRIMARY KEY,
    site   TEXT NOT NULL REFERENCES sites(name) ON DELETE CASCADE
);
