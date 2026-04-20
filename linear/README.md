# Linear CLI

`linear-cli` is the Linear companion for this repo. It is intentionally small, aligned to the authenticated MCP schemas, and prints JSON-first results.

The current server negotiates MCP protocol `2024-11-05`.

## Quick start

```bash
bash linear/install.sh
linear-cli --help
linear-cli mcp discover
```

## Commands

### Auth

```bash
linear-cli auth login
linear-cli auth login --api-key <key>
linear-cli auth logout
linear-cli auth status
linear-cli auth status --api-key <key>
```

`auth login` stores credentials in `~/.config/linear-cli/credentials.json`. Use `--api-key` to persist a Linear API key instead of starting the browser flow.

### MCP discovery

```bash
linear-cli mcp discover
```

Returns the initialized MCP session info, including the negotiated protocol version, and the current tool list as JSON.

### Projects

```bash
linear-cli project list --team <team> --query <query> --limit 20
linear-cli project get --query <project name, ID, or slug> --include-resources
linear-cli project save --id <project-id> --summary "Updated summary"
```

Project list accepts the verified Linear MCP filters directly:
- `--team <team>`: team name or ID
- `--query <query>`: search project name
- `--state <state>`: project state filter
- `--initiative <initiative>`: initiative name or ID
- `--member <member>`: user ID, name, email, or `me`
- `--label <label>`: single label filter
- `--cursor <cursor>`, `--limit <n>`, `--order-by createdAt|updatedAt`
- `--created-at <iso date or duration>` and `--updated-at <iso date or duration>`
- `--include-milestones`, `--include-members`, `--include-archived`

`project get` maps directly to `get_project(query)`. Use `--query`, not the stale `--project-id` contract.

`project save` is the only shipped write command. It maps 1:1 to `save_project`, and it is the command to use for live verification.

Write safety caveats:
- `project save --id <project-id>` updates an existing project.
- Creating a new project requires `--name` plus at least one team assignment flag such as `--add-team` or `--set-team`.
- `--labels`, `--add-team`, `--remove-team`, `--set-team`, `--add-initiative`, `--remove-initiative`, and `--set-initiative` are repeatable.
- Keep the command scoped to disposable or intended records only.

### Comments

```bash
linear-cli comment list --issue-id <issue-id> --cursor <cursor> --limit <n> --order-by createdAt
```

`--issue-id` is required. It accepts either an issue UUID or a Linear issue key such as `ENG-123`.

## Output

All command results are JSON-first. Success responses include `ok: true`. Errors include `ok: false` and an `error` object with a message, optional code, and optional details.

## Auth and config precedence

Auth credentials resolve in this order:
1. Explicit `--api-key`
2. `LINEAR_API_KEY`
3. Persisted credentials at `~/.config/linear-cli/credentials.json`

Team defaults resolve in this order:
1. Explicit `--team`
2. `LINEAR_DEFAULT_TEAM`
3. Persisted defaults at `~/.config/linear-cli/config.json`

Environment overrides:
- `LINEAR_API_KEY`
- `LINEAR_DEFAULT_TEAM`

## What is implemented now

This CLI ships only:
- `auth login`
- `auth logout`
- `auth status`
- `mcp discover`
- `project list`
- `project get --query <query>`
- `project save`
- `comment list --issue-id <issue-id>`

Everything else remains intentionally out of scope until the authenticated Linear tool inventory is confirmed and a safer write path is needed.

## Skill file

Agent guidance lives in `skill/linear/SKILL.md`.
