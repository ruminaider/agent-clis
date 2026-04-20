---
name: linear
description: Use when the user wants to work in Linear through `linear-cli`: authenticate, inspect MCP capabilities, list or fetch projects, update a project, or list comments on an issue. Also use when the user explicitly asks for `linear-cli`. Do NOT use for Linear's built-in connector, MCP plugin, or unrelated local files.
---

# Linear CLI

## Overview
Use `linear-cli` when you want terminal-based access to Linear through the repo's shipped CLI. Keep to the implemented command surface, use explicit identifiers, and verify the target before any write.

## Core Philosophy
**Trust the shipped CLI surface, not guessed commands.** Only use the commands documented below.

**Use explicit identifiers.** Prefer `--query` for project lookup and `--issue-id` for comment listing. Do not invent old flags like `--project-id`.

**Read before write.** Fetch the project first, then run `project save` only for an intended or disposable target.

**Keep writes reversible.** For verification or low-risk updates, prefer small fields like summary or color that can be restored immediately.

## Domain Mechanics
1. Check auth with `linear-cli auth status`. Use `linear-cli auth login` for OAuth, or `linear-cli auth login --api-key <key>` to persist an API key.
2. Inspect the live MCP surface with `linear-cli mcp discover` when you need to confirm the current server capabilities.
3. List projects with `linear-cli project list` and optional filters such as `--team`, `--query`, `--state`, `--initiative`, `--member`, `--label`, `--created-at`, `--updated-at`, `--limit`, and `--order-by`.
4. Fetch one project with `linear-cli project get --query <project name, ID, or slug>`.
5. List comments with `linear-cli comment list --issue-id <issue UUID or key>`.
6. Update a project with `linear-cli project save`. For updates, pass `--id <project-id>` plus only the fields you intend to change.
*Judgment:* If the target project is ambiguous, stop and ask for a direct project identifier instead of guessing.

## Auth and defaults
Auth precedence:
1. Explicit `--api-key`
2. `LINEAR_API_KEY`
3. Persisted credentials in `~/.config/linear-cli/credentials.json`

Team default precedence for project listing:
1. Explicit `--team`
2. `LINEAR_DEFAULT_TEAM`
3. Persisted defaults in `~/.config/linear-cli/config.json`

## Current command surface
- `linear-cli auth login`
- `linear-cli auth login --api-key <key>`
- `linear-cli auth logout`
- `linear-cli auth status`
- `linear-cli auth status --api-key <key>`
- `linear-cli mcp discover`
- `linear-cli project list`
- `linear-cli project get --query <query>`
- `linear-cli project save`
- `linear-cli comment list --issue-id <issue-id>`

## Common Mistakes
- Using `--project-id` with `project get` instead of `--query`
- Treating `LINEAR_API_KEY` as persisted auth. It is ephemeral unless you run `auth login --api-key <key>`
- Writing to a project before reading it first
- Assuming issue mutation, comment mutation, or other Linear commands exist in this CLI when they are not part of the shipped surface
