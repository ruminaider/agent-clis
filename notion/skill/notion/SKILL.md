---
name: notion
description: Interact with a Notion workspace via CLI. Search, read, create, and edit pages; manage databases, comments, users, and teams.
compatibility: Requires Node.js. Run /notion-setup or `notion-cli auth` to authenticate.
---

# Notion CLI

Access your full Notion workspace from the terminal with `notion-cli`. No integration setup, no page sharing required.

## Setup

```bash
notion-cli auth           # Refresh credentials, then open browser if needed
notion-cli auth status    # Check auth status
```

Credentials are stored at `~/.config/notion-cli/credentials.json`. You can also run `/notion-setup` in pi.

## Commands

### Search
```bash
notion-cli search "query"                    # Semantic search across workspace
```

### Fetch page/database content
```bash
notion-cli fetch <page-id>                   # Fetch by ID
notion-cli fetch "https://notion.so/..."     # Fetch by URL
```

### Pages
```bash
notion-cli page create --parent <id> --title "Title" [--content "markdown"]
notion-cli page update <page-id> [--title "New Title"] [--content "markdown"]
notion-cli page move <page-id> --parent <new-parent-id>
notion-cli page duplicate <page-id>
```

### Databases
```bash
notion-cli db create --parent <id> --title "Tasks" --schema "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
notion-cli db update <data-source-id> --schema "ALTER TABLE ..."
```

The `--schema` flag accepts SQL DDL syntax. `--ddl` still works as an alias.

### Comments
```bash
notion-cli comment list <page-id>            # List comments and discussions
notion-cli comment add <page-id> "text"      # Add a comment
```

### Users and Teams
```bash
notion-cli users                             # List workspace users
notion-cli teams                             # List workspace teams
```

### Debug
```bash
notion-cli tools                             # List available MCP tools
```

## Output

All commands return JSON. Pipe to `jq` for filtering:

```bash
notion-cli search "roadmap" | jq '.results[].title'
notion-cli users | jq '.results[] | select(.type=="person") | .name'
```

## Gotchas

- Page IDs work with or without hyphens.
- Search matches content, not just titles.
- If you get auth errors, run `notion-cli auth` to re-authenticate.
- `notion-cli auth status` shows token expiry and last refresh time.
