# Notion CLI

Full Notion workspace access from the terminal. Authenticates via Notion's MCP OAuth — just run `auth login`, approve in browser, done. No integration setup, no page sharing.

## Install

```bash
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

All commands output JSON. Pipe to `jq`:

```bash
notion-cli search "roadmap" | jq '.results[].title'
```

## How it works

Instead of using the Notion API directly (which requires creating integrations and sharing pages), this CLI authenticates through Notion's remote MCP server OAuth. This gives you the same full workspace access that MCP connectors in Claude/Cursor have — simple approve/deny, no page picker, no admin setup.

The CLI then routes all operations through `mcp.notion.com`, which handles the Notion API calls server-side.
