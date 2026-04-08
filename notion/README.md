# Notion CLI

Access your full Notion workspace from the terminal. Authenticates via Notion's MCP OAuth: run `auth login`, approve in browser, done. You create no integrations and share no pages.

## Install

```bash
# Via npm
npm i -g @ruminaider/notion-cli

# Or from repo clone
bash install.sh
```

## Authenticate

```bash
notion-cli auth login     # Opens browser → click Approve → done
notion-cli auth status    # Check auth status
```

## Commands

```bash
# Search
notion-cli search "quarterly roadmap"

# Read pages
notion-cli fetch <page-id-or-url>

# Create/edit pages
notion-cli page create --parent <id> --title "Title" --content "markdown content"
notion-cli page update <page-id> --title "New Title" --content "updated content"
notion-cli page move <page-id> --parent <new-parent-id>
notion-cli page duplicate <page-id>

# Databases
notion-cli db create --parent <id> --ddl "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
notion-cli db update <data-source-id> --ddl "ALTER TABLE ..."

# Comments
notion-cli comment list <page-id>
notion-cli comment add <page-id> "comment text"

# Users & teams
notion-cli users
notion-cli teams
```

## Output

All commands return JSON. Pipe to `jq` for filtering:

```bash
notion-cli search "roadmap" | jq '.results[].title'
```

## How It Works

The Notion API requires creating integrations and sharing individual pages. This CLI bypasses both steps by authenticating through Notion's remote MCP server OAuth. You get the same full workspace access that MCP connectors in Claude and Cursor provide: one approve/deny prompt, no page picker, no admin setup.

All operations route through `mcp.notion.com`, which handles Notion API calls server-side.
