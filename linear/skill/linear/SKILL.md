---
name: linear
description: Use when the user needs to inspect Linear MCP connectivity, list or fetch projects, or list issue comments from the terminal. This is still a scaffold. Limit usage to the verified MVP surface until authenticated tool inventory is confirmed.
compatibility: Requires Node.js. Run `linear-cli auth` once the implementation lands. Planned auth precedence: explicit CLI flags, environment variables, then `~/.config/linear-cli/config.json`.
---

# Linear CLI

## Overview
`linear-cli` is the planned Linear companion CLI. This wave reserves the package shape and the safe seams for auth, transport, config, and identifier handling.

## Verified MVP target surface
Use only these commands as the initial implementation target until authenticated `tools/list` inventory is confirmed:
- `auth`
- `mcp discover`
- `project list`
- `project get <project-id>`
- `comment list <issue-id>`

## Config and auth contract
- Persisted defaults will live in `~/.config/linear-cli/config.json`.
- Credentials will live in `~/.config/linear-cli/credentials.json`.
- Precedence order: explicit CLI flags, environment variables, then persisted config.
- Planned environment overrides: `LINEAR_API_KEY`, `LINEAR_DEFAULT_TEAM`, `LINEAR_DEFAULT_WORKSPACE`.

## Identifier handling seam
- Accept direct identifiers first, especially IDs and stable keys.
- Resolve human-friendly names through tool calls only after authenticated capability checks are available.
- Fail clearly on ambiguity. Do not guess between multiple matches.

## Deferred until authenticated inventory is confirmed
- Search commands
- Issue create or update flows
- Project create or update flows
- Label or initiative commands
- Any command that depends on unverified tool schemas
