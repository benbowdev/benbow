# MCP server

`covo mcp` serves the Model Context Protocol over stdin/stdout, so AI agents
can work with Covo:

```sh
claude mcp add covo -- covo mcp
```

(In the app, Settings → General → AI agents shows the command with the full
path of the `covo` inside the app.)

| Tool | Changes | What |
|---|---|---|
| `covo_status` | no | running, services, sites with addresses, MySQL, Mail |
| `list_sites` | no | every site |
| `site_logs` | no | end of a site's PHP error log or request log |
| `list_php_versions` | no | installed and available versions, php.net support status |
| `php_compatibility_check` | no | what would break on another PHP version (`php.md`) |
| `list_databases` | no | databases on Covo's MySQL |
| `start`, `stop` | yes | all sites and services |
| `add_site` | yes | folder, PHP, domain, aliases, new folder, database, php.ini values |
| `update_site` | yes | PHP version, folder, domain, aliases, on/off |
| `create_site_database` | yes | database with its own user (password returned once) |
| `new_wordpress` | yes | WordPress in one step (admin password returned once) |
| `wp` | yes | wp-cli with the site's PHP; output and exit code |

Like the CLI, the server is a thin client of the core, opened for each call.
On purpose there is no tool that removes a site, a database or a group:
deleting stays with the user. `wp` refuses `--prompt` and `shell` (they need
a terminal); other wp-cli commands can change or delete data, and the tool
says so in its annotations. Output is cut at 100 KiB; a wp-cli run stops
after 10 minutes. Errors come back as tool errors starting with Covo's error
code (`not_found: …`).

Built with the official Go SDK (`github.com/modelcontextprotocol/go-sdk`).
