# Blueprints

A blueprint is a JSON template for WordPress sites: WordPress version and
language, themes, plugins, options, wp-config constants and must-use
plugins. Use one for a new site, apply one to an existing site, or save a
site as one.

```sh
benbow blueprint import agency.json            # add (or --name other, --force to replace)
benbow wordpress shop --blueprint agency       # new site from it (asks first; --yes)
benbow blueprint apply agency shop [--dry-run] # into an existing WordPress site
benbow blueprint save shop agency-2            # from a site
benbow blueprint list | show <name> | export <name> [file] | delete <name>
benbow blueprint sources                       # downloads and their SHA-256
benbow blueprint github-token [--delete]       # for private GitHub repositories
```

In the app: New Site → WordPress → Blueprint; a WordPress site's Advanced →
Blueprints (apply, save); Settings → Blueprints (import, export, delete,
GitHub token).

## Format

```json
{
  "name": "agency",
  "description": "Our starting point",
  "wordpress": { "version": "6.8.3", "locale": "it_IT" },
  "themes": [
    { "slug": "astra", "activate": true }
  ],
  "plugins": [
    { "slug": "contact-form-7", "version": "6.1" },
    { "slug": "query-monitor", "activate": false },
    { "url": "https://example.com/our-plugin.zip" },
    { "github": "acme/acme-tools", "ref": "v2.1.0" },
    { "git": "git@github.com:acme/private.git", "ref": "main", "slug": "private" }
  ],
  "options": {
    "permalinks": "/%postname%/",
    "timezone": "Europe/Rome",
    "remove_sample_content": true,
    "set": { "blogdescription": "Just another Benbow site" }
  },
  "constants": { "WP_DEBUG": true, "WP_MEMORY_LIMIT": "256M" },
  "mu_plugins": [
    { "name": "disable-emojis.php", "content": "<?php remove_action('wp_head', 'print_emoji_detection_script', 7);" }
  ]
}
```

Every field but `name` is optional; unknown fields are refused.

- **wordpress**: `version` ("" = the WordPress Benbow pins; another version
  goes through `wp core update`, which checks wordpress.org's checksums);
  `locale` installs and activates that language.
- **themes, plugins**: one source each:
  - `slug` alone: wordpress.org, at `version` or the latest one (Benbow asks
    wordpress.org which, and downloads that exact version);
  - `url`: a zip over HTTPS;
  - `github`: `owner/repo` at `ref` (branch, tag or commit; default the
    default branch), resolved to a commit first;
  - `git`: an `https://` or `git@` URL cloned with the system git (only if
    Apple's Command Line Tools are installed; Benbow never installs them).
  The folder name is `slug`, else the repository name, else the zip's
  top folder. `activate` defaults to true for plugins, false for themes (at
  most one theme).
- **options**: `permalinks`, `timezone` (e.g. `Europe/Rome`),
  `remove_sample_content` (the sample post and page, the draft privacy
  page, Hello Dolly and Akismet), `set` for other options. `siteurl`,
  `home`, `admin_email`, `active_plugins`, `template` and `stylesheet`
  cannot be set.
- **constants**: `wp config set` (text, numbers, true/false). The database
  settings, keys, salts and `ABSPATH` cannot be set.
- **mu_plugins**: files written into `wp-content/mu-plugins` (start with
  `<?php`, under 256 KiB each).

## Safety

- A blueprint installs third-party code: Benbow says so before applying
  (CLI question or `--yes`, a note in the app).
- Benbow downloads every theme and plugin itself and records its SHA-256 the
  first time (`benbow blueprint sources`), under the exact URL: wordpress.org
  version, GitHub commit, git commit (for git, the hash of the files). If
  the same source later has other content, Benbow stops before installing it;
  `--accept-changes` (or "Accept Changes and Apply") records the new one.
- Zips: no absolute paths, `..`, links or special files; at most 200 MiB
  downloaded, 500 MiB and 20,000 files unpacked. Git clones run without
  hooks or submodules, and `.git` is removed.
- Applying to an existing site takes a snapshot first (`snapshots.md`); on
  failure the error names the snapshot to restore. Themes, plugins and
  must-use plugins already there are left as they are. For a new site, a
  failure removes the whole site again.
- The GitHub token lives in the login keychain (`dev.benbow.github`,
  removed by `benbow uninstall`) and is sent only to api.github.com.
- `benbow blueprint save` keeps wordpress.org themes and plugins at their
  installed versions; others come back as warnings (add their `url` or
  `github` by hand). Constants are not saved: they can hold secrets.
  Must-use plugins are saved with their content.

## Notes

- wp-cli runs some commands again in a child shell with its own path
  unquoted, which broke on Benbow's folder ("Application Support"). Benbow
  loads a small prepend file for wp-cli (`data/wp-cli/launch.php`) that
  quotes it.
- Laravel blueprints may come later.
