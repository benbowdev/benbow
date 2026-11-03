CREATE TABLE blueprints (
    name TEXT PRIMARY KEY,
    data TEXT NOT NULL,
    created TEXT NOT NULL,
    updated TEXT NOT NULL
);
CREATE TABLE blueprint_sources (
    url TEXT PRIMARY KEY,
    sha256 TEXT NOT NULL,
    first_used TEXT NOT NULL,
    last_used TEXT NOT NULL
);
