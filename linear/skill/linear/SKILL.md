---
name: linear
description: Use when you need to authenticate with Linear MCP, inspect MCP capabilities, list projects, fetch a project, or list comments for an issue. This skill is intentionally limited to the current MVP surface.
compatibility: Requires Node.js. Auth precedence is explicit CLI flags, environment variables, then persisted config.
---

# Linear CLI

## What this tool does

`linear-cli` is the current MVP companion for Linear. It supports only the shipped read-only workflow surface:
- `auth login`
- `auth logout`
- `auth status`
- `mcp discover`
- `project list`
- `project get --project-id <project-id>`
- `comment list --issue-id <issue-id>`

Do not assume write commands or additional namespaces exist yet.

## Auth and configuration

Use explicit flags first, then environment variables, then persisted config.

`auth login --api-key <key>` persists a Linear API key without starting the browser flow.

Environment overrides:
- `LINEAR_API_KEY`
- `LINEAR_DEFAULT_TEAM`
- `LINEAR_DEFAULT_WORKSPACE`

Credentials are stored in `~/.config/linear-cli/credentials.json`.
Persistent defaults are stored in `~/.config/linear-cli/config.json`.

## Operating rules

- Prefer explicit identifiers. Pass `--project-id` for projects and `--issue-id` for comments.
- `--issue-id` accepts either an issue UUID or a Linear issue key like `ENG-123`.
- Do not guess between multiple matches. If an identifier is unclear, stop and ask for a direct ID.
- Use `mcp discover` when you need to inspect the current session or verify available tools.
- Keep output handling simple. `linear-cli` prints JSON-first results, so prefer parsing JSON over reading formatted text.
- Stay within the current MVP. If a task asks for a command that is not listed above, treat it as future expansion and do not invent it.

## Suggested command patterns

```bash
linear-cli auth status
linear-cli mcp discover
linear-cli project list --team <team> --workspace <workspace>
linear-cli project get --project-id <project-id>
linear-cli comment list --issue-id <issue-id> --limit 20
```

## Current scope boundary

This skill is for the linear/cli MVP only. It intentionally excludes issue mutation, project mutation, and any unverified Linear MCP tool until the authenticated inventory is confirmed.
