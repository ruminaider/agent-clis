# Linear CLI

`linear-cli` is the current MVP for the Linear tool. It is intentionally small: auth, MCP discovery, project reads, and issue comment reads only.

## Quick start

```bash
bash install.sh
linear-cli --help
```

## Commands

### Auth

```bash
linear-cli auth login
linear-cli auth logout
linear-cli auth status
```

`auth login` stores credentials in `~/.config/linear-cli/credentials.json`.

### MCP discovery

```bash
linear-cli mcp discover
```

Returns the initialized MCP session info and current tool list as JSON.

### Projects

```bash
linear-cli project list --team <team> --workspace <workspace> --api-key <key>
linear-cli project get --project-id <project-id> --api-key <key>
```

Notes:
- `--team` and `--workspace` override the default context for the command.
- `--api-key` can be used instead of browser auth for a single invocation or for `auth status` checks.
- `project get` currently requires an explicit `--project-id` flag. A positional ID is accepted for compatibility, but the flag is preferred.

### Comments

```bash
linear-cli comment list --issue-id <issue-id> --cursor <cursor> --limit <n> --order-by <field> --api-key <key>
```

Notes:
- `--issue-id` is required.
- `--cursor`, `--limit`, and `--order-by` are passed through to the verified comment listing tool.

## Output

All command results are JSON-first. Success responses include `ok: true`. Errors include `ok: false` and an `error` object with a message, optional code, and optional details.

## Auth and config precedence

The current implementation resolves credentials and defaults in this order:
1. Explicit CLI flags
2. Environment variables
3. Persisted config at `~/.config/linear-cli/config.json`

Environment overrides:
- `LINEAR_API_KEY`
- `LINEAR_DEFAULT_TEAM`
- `LINEAR_DEFAULT_WORKSPACE`

## What is implemented now

This MVP only includes:
- `auth login`
- `auth logout`
- `auth status`
- `mcp discover`
- `project list`
- `project get --project-id <project-id>`
- `comment list --issue-id <issue-id>`

Everything else remains intentionally out of scope until the authenticated Linear tool inventory is confirmed.

## Skill file

Agent guidance lives in `skill/linear/SKILL.md`.
