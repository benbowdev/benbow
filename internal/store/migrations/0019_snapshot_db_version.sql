-- The version of the database server a snapshot's dump came from, to
-- explain a restore that fails. Empty for older snapshots.
ALTER TABLE snapshots ADD COLUMN db_version TEXT NOT NULL DEFAULT '';
