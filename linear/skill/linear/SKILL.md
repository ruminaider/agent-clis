---
name: linear
description: "Use when the user wants to work in Linear through `linear-cli`: authenticate, inspect MCP capabilities, or read and write any supported entity (projects, issues, comments, attachments, cycles, documents, milestones, teams, users, labels, statuses). Also use when the user explicitly asks for `linear-cli`. Do NOT use for Linear's built-in connector, MCP plugin, or unrelated local files."
---

# Linear CLI

## Overview
Use `linear-cli` when you want terminal-based access to Linear through the repo's shipped CLI. The command surface mirrors the authenticated Linear MCP server 1:1. Keep to the implemented commands, use explicit identifiers, and verify the target before any write.

## Core Philosophy
**Trust the shipped CLI surface, not guessed commands.** The CLI covers every tool the MCP server exposes; if you need something else, confirm it first with `linear-cli mcp discover`.

**Use explicit identifiers.** Prefer `--query` for project and team lookup, `--id` for entities with keys or UUIDs, and `--issue-id` for comment listing. Do not invent flags.

**Read before write.** Fetch the entity first, then write only the fields you intend to change.

**Resolve enums before setting them.** Before `--state`, list statuses for the team. Before `--assignee`, resolve the user. Before `--project` on an issue, fetch the project.

## Domain Mechanics
1. Check auth with `linear-cli auth status`. Use `linear-cli auth login` for OAuth, or `linear-cli auth login --api-key <key>` to persist an API key.
2. Inspect the live MCP surface with `linear-cli mcp discover` when you need to confirm the current server capabilities.
3. **Projects.** `linear-cli project list`, `project get --query`, `project save`, `project-label list`. `project save` requires `--id` for updates or `--name` plus at least one team flag for creation.
4. **Issues.** `linear-cli issue list`, `issue get --id`, `issue save`.
   - Create: `issue save --title <title> --team <team> [fields]`. Do not pass `--id`.
   - Update: `issue save --id <issue-id> [fields]`. `--id` is a UUID or key like `LIN-123`.
   - Relations (append-only): `--link "url|title"`, `--blocks`, `--blocked-by`, `--related-to`. Use `--duplicate-of <issue>` to mark a duplicate.
   - Relation removal: `--remove-blocks`, `--remove-blocked-by`, `--remove-related-to` (repeatable).
   - Null-to-remove: use `--clear-assignee`, `--clear-delegate`, `--clear-parent`, `--clear-duplicate-of` to pass a literal null to the MCP server. Each clear flag is mutually exclusive with its value flag.
5. **Issue metadata.** Resolve workflow states with `linear-cli status list --team <team>` and inspect one with `status get --id --name --team` (all three required). Manage labels with `issue-label list` and `issue-label create --name [--team-id] [--color] [--parent] [--is-group]`.
6. **Comments.** `comment list --issue-id`, `comment save` (create with `--issue-id --body`, update with `--id --body`, reply with `--issue-id --parent-id --body`), `comment delete --id`.
7. **Attachments.** `attachment get --id`, `attachment create --issue <id> --file <path>` (the CLI base64-encodes the file and infers filename and MIME type), `attachment delete --id`.
8. **Cycles.** `cycle list --team-id <team-uuid> [--type current|previous|next]`. `--team-id` must be a UUID, not a team name or key.
9. **Milestones.** `milestone list --project`, `milestone get --project --query`, `milestone save --project [--id | --name] [--description] [--target-date | --clear-target-date]`.
10. **Documents.** `document list`, `document get --id <id-or-slug>`, `document create --title [--content] [--project | --issue]`, `document update --id [fields]`. Documents attach to a project or an issue, never both.
11. **Teams and users.** `team list`, `team get --query`, `user list`, `user get --query` (accepts UUID, name, email, or `me`).
12. **Utilities.** `image extract --markdown <content>` or `--from-file <path>` to resolve embedded images. `docs search --query <text>` to search Linear's help docs.

*Judgment:* If the target entity is ambiguous, stop and ask for a direct identifier instead of guessing. For writes, prefer a dry read-then-write cycle: fetch the entity, plan the change, then call `save`. Use `<command> <subcommand> --help` (for example, `linear-cli issue save --help`) when you need only that command's flag reference rather than the full CLI help.

## Output Contract
- Every command prints JSON to stdout.
- Success: `{ ok: true, command, ...data }`.
- CLI-level error: `{ ok: false, error: { message, code, details } }` with exit code 1.
- Tool-level error from MCP (for example, "Entity not found"): `{ ok: true, command, result: "<server message>" }`. Always inspect the body; `ok: true` means the CLI reached the server, not that the operation succeeded.

## Auth and defaults
Auth precedence:
1. Explicit `--api-key`
2. `LINEAR_API_KEY`
3. Persisted credentials in `~/.config/linear-cli/credentials.json`

Team default precedence for project and issue listing:
1. Explicit `--team`
2. `LINEAR_DEFAULT_TEAM`
3. Persisted defaults in `~/.config/linear-cli/config.json`

## Current command surface
- `linear-cli auth login [--api-key <key>]`
- `linear-cli auth logout`
- `linear-cli auth status [--api-key <key>]`
- `linear-cli mcp discover`
- `linear-cli project list|get|save`
- `linear-cli project-label list`
- `linear-cli issue list|get|save`
- `linear-cli issue-label list|create`
- `linear-cli status list|get`
- `linear-cli comment list|save|delete`
- `linear-cli attachment get|create|delete`
- `linear-cli cycle list`
- `linear-cli document list|get|create|update`
- `linear-cli milestone list|get|save`
- `linear-cli team list|get`
- `linear-cli user list|get`
- `linear-cli image extract`
- `linear-cli docs search`

## Common Mistakes
- Using `--project-id` with `project get` instead of `--query`.
- Passing a team name to `cycle list`. `--team-id` must be a UUID.
- Omitting `--team` when creating an issue, or omitting `--id` when updating.
- Passing a state name that does not exist for the target team. Verify with `status list --team <team>` first.
- Expecting to clear a field by passing an empty string. Empty values are stripped. Use the matching `--clear-*` flag for explicit nulls, or the Linear UI when no clear flag exists.
- Combining a value flag and its `--clear-*` flag in the same call. They are mutually exclusive.
- Treating `LINEAR_API_KEY` as persisted auth. It is ephemeral unless you run `auth login --api-key <key>`.
- Writing to an entity before reading it first.
- Trusting `ok: true` alone. Inspect `result` for server-side error messages.
- Attempting to hard-delete an issue. Linear's MCP surface has no `delete_issue` tool; cancel the issue by setting `--state Canceled` instead.
