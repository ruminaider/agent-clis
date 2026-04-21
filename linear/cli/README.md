# @ruminaider/linear-cli

JSON-first CLI access to Linear through its authenticated remote MCP server. Every command maps 1:1 to a Linear MCP tool.

## Command surface

- `linear-cli auth login|logout|status` (with optional `--api-key <key>`)
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

Run `linear-cli help` for the full flag reference.

## Install from repo

```bash
bash linear/install.sh
```

## Auth precedence

1. Explicit `--api-key`
2. `LINEAR_API_KEY`
3. Persisted credentials in `~/.config/linear-cli/credentials.json`

Team defaults resolve separately:

1. Explicit `--team`
2. `LINEAR_DEFAULT_TEAM`
3. Persisted defaults in `~/.config/linear-cli/config.json`

## Write commands

- `project save` → `save_project` (create or update a project)
- `issue save` → `save_issue` (create or update an issue, plus relation append and removal)
- `issue-label create` → `create_issue_label`
- `comment save` / `comment delete` → `save_comment` / `delete_comment`
- `attachment create` / `attachment delete` → `create_attachment` / `delete_attachment`
- `document create` / `document update` → `create_document` / `update_document`
- `milestone save` → `save_milestone`

See the package README for examples and the per-flag reference for each command.

## End-to-end regression test

`cli/scripts/e2e.sh` exercises the full skill workflow against a real Linear workspace (create issue, assign and clear, add/delete comment, attach/delete file, filter by unassigned, cancel the issue). Run it before each release:

```bash
LINEAR_DEFAULT_TEAM=<team> npm run e2e
# or: bash cli/scripts/e2e.sh --team <team>
# add --keep to skip the Canceled state cleanup when debugging a failure
```

The script requires a usable auth credential (OAuth or `LINEAR_API_KEY`) and writes only to a disposable test issue clearly titled "linear-cli e2e ... (safe to delete)".

## Notes

- Output is JSON-first. Tool-level errors returned by the server (for example, "Entity not found") surface under `result` with `ok: true`; always inspect the body.
- The current Linear MCP session negotiates protocol `2024-11-05`.
- Null-to-remove semantics on `save_issue` and `save_project` are not reachable through the CLI. Use the Linear UI for explicit clears.
