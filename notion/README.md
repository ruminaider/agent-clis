# Notion CLI

Access your full Notion workspace from the terminal. No integrations to create, no pages to share.

## Quick Start

```bash
npm i -g @ruminaider/notion-cli
notion-cli auth
notion-cli search "quarterly roadmap"
```

## Install

```bash
# Via npm
npm i -g @ruminaider/notion-cli

# Or from repo clone
bash install.sh
```

## Authenticate

```bash
notion-cli auth           # Refresh token first; open browser only if needed
notion-cli auth status    # Show token age, expiry, and last refresh
```

Credentials live at `~/.config/notion-cli/credentials.json`.

## Commands

```bash
# Search
notion-cli search "quarterly roadmap"

# Read pages
notion-cli fetch <page-id-or-url>

# Create, edit, move, duplicate pages
notion-cli page create --parent <id> --title "Title" --content "markdown content"
notion-cli page update <page-id> --title "New Title" --content "updated content"
notion-cli page move <page-id> --parent <new-parent-id>
notion-cli page duplicate <page-id>

# Databases
notion-cli db create --parent <id> --title "Tasks" --schema "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
notion-cli db update <data-source-id> --schema "ALTER TABLE ..."
# --ddl is still accepted as a fallback for --schema

# Comments
notion-cli comment list <page-id>
notion-cli comment add <page-id> "comment text"

# Users and teams
notion-cli users
notion-cli teams
```

## Output

All commands return JSON. Pipe to `jq` for filtering:

```bash
notion-cli search "roadmap" | jq '.results[].title'
```

## How It Works

The Notion API normally requires you to create an integration and share each page with it. This CLI skips both steps by authenticating through Notion's remote MCP server OAuth: one approve/deny prompt grants full workspace access, the same access that MCP connectors in Claude and Cursor provide.

All operations route through `mcp.notion.com`, which handles Notion API calls server-side.
