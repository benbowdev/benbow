-- Order of sites and groups in the list, chosen by the user (drag and
-- drop). 0 everywhere means "by name"; new items go last once a level
-- has been ordered.
ALTER TABLE sites ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
ALTER TABLE site_groups ADD COLUMN position INTEGER NOT NULL DEFAULT 0;
