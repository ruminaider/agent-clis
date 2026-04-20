---
name: linear
description: Use when you need to authenticate with Linear MCP, inspect MCP capabilities, list projects, fetch a project, update a project, or list comments for an issue. This skill is intentionally limited to the shipped Linear CLI surface.
compatibility: Requires Node.js. Auth precedence is explicit `--api-key`, then `LINEAR_API_KEY`, then persisted credentials in `~/.config/linear-cli/credentials.json`. Team defaults resolve separately from `~/.config/linear-cli/config.json`.
---

# Linear CLI

## What this tool does

`linear-cli` is the current Linear companion for this repo. It supports only the shipped surface below:
- `auth login`
- `auth logout`
- `auth status`
- `mcp discover`
- `project list`
- `project get --query <query>`
- `project save`
- `comment list --issue-id <issue-id>`

Do not assume any other Linear mutation command is available.

## Auth and configuration

Auth precedence:
1. Explicit `--api-key`
2. `LINEAR_API_KEY`
3. Persisted credentials in `~/.config/linear-cli/credentials.json`

Team default precedence for project listing:
1. Explicit `--team`
2. `LINEAR_DEFAULT_TEAM`
3. Persisted defaults in `~/.config/linear-cli/config.json`

`auth login --api-key <key>` persists a Linear API key without starting the browser flow.

Environment overrides:
- `LINEAR_API_KEY`
- `LINEAR_DEFAULT_TEAM`

## Operating rules

- Prefer explicit identifiers. Pass `--query` for project lookup and `--issue-id` for comment listing.
- `project get` uses the real MCP schema field `query`. Do not use the old `--project-id` contract.
- `project list` can filter by `--created-at` and `--updated-at` timestamps or durations.
- `--issue-id` accepts either an issue UUID or a Linear issue key like `ENG-123`.
- Use `project save` only when a write is required and the target is intended or disposable.
- `project save` is the only shipped write command. It maps directly to `save_project`, and creating a project requires `--name` plus at least one team assignment flag.
- If a project change is ambiguous, stop and ask for a direct project identifier instead of guessing.
- Use `mcp discover` when you need to inspect the current session or verify available tools.
- The Linear MCP session currently negotiates protocol `2024-11-05`.
- Keep output handling simple. `linear-cli` prints JSON-first results, so prefer parsing JSON over reading formatted text.

## Suggested command patterns

```bash
linear-cli auth status
linear-cli mcp discover
linear-cli project list --team <team> --query <query>
linear-cli project get --query <project name, ID, or slug>
linear-cli project save --id <project-id> --summary "Updated summary"
linear-cli comment list --issue-id <issue-id> --limit 20
```

## Current scope boundary

This skill is for the Linear CLI MVP only. It intentionally excludes issue mutation, comment mutation, and any unverified Linear MCP tool until the authenticated inventory is confirmed and a safer write path is needed.
