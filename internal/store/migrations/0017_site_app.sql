-- Sites of kind "app": a dev server (any language) that Benbow starts with the
-- user's own command, or only proxies to, behind the site's HTTPS domain.
-- PHP sites keep kind 'php' and ignore the other columns, except the
-- companion: a command started and stopped together with a PHP site.
ALTER TABLE sites ADD COLUMN kind TEXT NOT NULL DEFAULT 'php';
ALTER TABLE sites ADD COLUMN app_mode TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN command TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN app_port INTEGER NOT NULL DEFAULT 0;
ALTER TABLE sites ADD COLUMN app_host TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN companion_command TEXT NOT NULL DEFAULT '';
ALTER TABLE sites ADD COLUMN companion_port INTEGER NOT NULL DEFAULT 0;
