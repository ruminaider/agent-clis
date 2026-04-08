---
name: notion
description: Interact with Notion workspace via CLI. Search pages, read/create/edit pages, manage databases, comments, users, and teams. Use when the user needs to work with Notion content.
compatibility: Requires Node.js. Run /notion-setup or 'notion-cli auth login' to authenticate.
---

# Notion CLI

Access your full Notion workspace from the terminal using `notion-cli`. Authenticates via Notion's MCP OAuth — just run `login`, authorize in browser, done. No integration setup, no page sharing, full workspace access.

## Setup

```bash
notion-cli auth login     # Opens browser → approve → done
notion-cli auth status    # Check auth status
```

Or use `/notion-setup` in pi.

## Commands

### Search
```bash
notion-cli search "query"                    # Search workspace (semantic)
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
notion-cli db create --parent <id> --ddl "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
notion-cli db update <data-source-id> --ddl "ALTER TABLE ..."
```

### Comments
```bash
notion-cli comment list <page-id>            # List comments/discussions
notion-cli comment add <page-id> "text"      # Add comment
```

### Users & Teams
```bash
notion-cli users                             # List workspace users
notion-cli teams                             # List workspace teams
```

### Debug
```bash
notion-cli tools                             # List available MCP tools
```

## Output

All commands output JSON. Pipe to `jq` for filtering:

```bash
notion-cli search "roadmap" | jq '.results[].title'
notion-cli users | jq '.results[] | select(.type=="person") | .name'
```

## Gotchas

- Token expires after ~60 minutes but auto-refreshes if a refresh token is available.
- If you get auth errors, re-run `notion-cli auth login`.
- Page IDs from URLs work with or without hyphens.
- The `fetch` command accepts both Notion URLs and raw page IDs.
- Database creation uses SQL DDL syntax (Notion MCP-specific).
- Search is semantic — it searches content, not just titles.
