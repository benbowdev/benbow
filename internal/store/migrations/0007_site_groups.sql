CREATE TABLE site_groups (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    parent_id INTEGER REFERENCES site_groups(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL
);
ALTER TABLE sites ADD COLUMN group_id INTEGER REFERENCES site_groups(id) ON DELETE SET NULL;
