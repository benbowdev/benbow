-- A free label shown in the list instead of the site's name (which stays
-- the identifier and the start of its address).
ALTER TABLE sites ADD COLUMN title TEXT NOT NULL DEFAULT '';
