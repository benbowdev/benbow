-- The site's PHP version and its own php.ini values when the snapshot was
-- taken: a restore can bring them back. Empty for older snapshots.
ALTER TABLE snapshots ADD COLUMN php_minor TEXT NOT NULL DEFAULT '';
ALTER TABLE snapshots ADD COLUMN php_settings TEXT NOT NULL DEFAULT '';
