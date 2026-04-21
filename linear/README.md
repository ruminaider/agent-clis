# Linear CLI

`linear-cli` is the Linear companion for this repo. It is a thin, JSON-first wrapper over Linear's authenticated remote MCP server. Every command maps 1:1 to a Linear MCP tool, so the CLI surface mirrors what the server exposes.

The current server negotiates MCP protocol `2024-11-05`.

## Quick start

```bash
bash linear/install.sh
linear-cli auth login
linear-cli --help
linear-cli mcp discover
linear-cli issue list --limit 10
```

## Command surface at a glance

| Domain | Commands |
| --- | --- |
| Auth | `auth login`, `auth logout`, `auth status` |
| Discovery | `mcp discover` |
| Projects | `project list`, `project get`, `project save`, `project-label list` |
| Issues | `issue list`, `issue get`, `issue save` |
| Issue metadata | `status list`, `status get`, `issue-label list`, `issue-label create` |
| Comments | `comment list`, `comment save`, `comment delete` |
| Attachments | `attachment get`, `attachment create`, `attachment delete` |
| Cycles | `cycle list` |
| Milestones | `milestone list`, `milestone get`, `milestone save` |
| Documents | `document list`, `document get`, `document create`, `document update` |
| Teams | `team list`, `team get` |
| Users | `user list`, `user get` |
| Images | `image extract` |
| Help docs | `docs search` |

Run `linear-cli help` for the combined flag reference. Every command and subcommand also supports `--help` for a focused help page, for example `linear-cli issue save --help`.

## Commands

### Auth

```bash
linear-cli auth login
linear-cli auth login --api-key <key>
linear-cli auth logout
linear-cli auth status
```

`auth login` stores credentials in `~/.config/linear-cli/credentials.json`. Use `--api-key` to persist a Linear API key instead of starting the OAuth browser flow. `auth status` reports the active credential type and, for OAuth, the token expiry.

### MCP discovery

```bash
linear-cli mcp discover
```

Returns the initialized MCP session info (protocol version, session ID, server capabilities) and the current tool list as JSON. Use it to confirm what the server exposes before writing new wrappers.

### Projects

```bash
linear-cli project list --team <team> --query <query> --limit 20
linear-cli project get --query <project name, ID, or slug> --include-resources
linear-cli project save --id <project-id> --summary "Updated summary"
linear-cli project save --id <project-id> --clear-lead
linear-cli project-label list --name <filter>
```

`project list` accepts every verified Linear MCP filter: `--team`, `--query`, `--state`, `--initiative`, `--member`, `--label`, `--created-at`, `--updated-at`, `--cursor`, `--limit`, `--order-by`, `--include-milestones`, `--include-members`, `--include-archived`.

`project save` maps to `save_project`. Creating a project requires `--name` plus at least one team assignment (`--add-team` or `--set-team`). Updating requires `--id`. `--labels`, `--add-team`, `--remove-team`, `--set-team`, `--add-initiative`, `--remove-initiative`, and `--set-initiative` are repeatable. Use `--clear-lead` to remove a project lead explicitly.

### Issues

```bash
linear-cli issue list --team ENG --assignee me --state "In Progress" --limit 20
linear-cli issue get --id ENG-123 --include-relations
linear-cli issue save --title "Flaky login" --team ENG --priority 2 --labels bug
linear-cli issue save --id ENG-123 --state "In Progress" --assignee me
linear-cli issue save --id ENG-123 --blocks ENG-100 --blocks ENG-101 --link "https://example.com/pr/5|PR #5"
```

`issue save` maps 1:1 to `save_issue` and handles both creation and update. Creating requires `--title` and `--team`. Updating requires `--id` (UUID or key like `ENG-123`).

| Flag | Notes |
| --- | --- |
| `--id` | Issue UUID or key. Required for update |
| `--title` | Required for create |
| `--team` | Team name or ID. Required for create |
| `--description` | Literal Markdown |
| `--project`, `--state`, `--assignee`, `--delegate`, `--cycle`, `--milestone` | Resolved by name, ID, or the tool-specific aliases (`me` for assignee) |
| `--priority` | `0` None, `1` Urgent, `2` High, `3` Normal, `4` Low |
| `--labels` | Repeatable |
| `--due-date`, `--parent`, `--estimate` | Straight pass-through |
| `--link "url\|title"` | Repeatable. Append-only. Existing links are never removed |
| `--blocks`, `--blocked-by`, `--related-to` | Repeatable. Append-only |
| `--duplicate-of` | Issue ID or key |
| `--remove-blocks`, `--remove-blocked-by`, `--remove-related-to` | Repeatable |
| `--clear-assignee`, `--clear-delegate`, `--clear-parent`, `--clear-duplicate-of` | Pass a literal null to the MCP server (Linear's "remove this value" semantics). Mutually exclusive with the matching value flag. |

Before writing, verify the team with `linear-cli team get --query <name>`, resolve the workflow state with `linear-cli status list --team <team>`, and confirm the project with `linear-cli project get --query <name>`.

### Issue metadata

```bash
linear-cli status list --team ENG
linear-cli status get --id <status-id> --name "In Progress" --team ENG
linear-cli issue-label list --team ENG
linear-cli issue-label create --name "needs-repro" --color "#FF9F1C" --team-id <team-uuid>
```

`status list` and `status get` wrap `list_issue_statuses` and `get_issue_status`. `status get` requires all three of `--id`, `--name`, and `--team` per the server schema.

`issue-label create` creates a team-scoped label when `--team-id` is set and a workspace-scoped label when it is omitted. `--parent <label-group>` nests the label under an existing group. `--is-group` marks the label itself as a group container rather than an applicable label.

### Comments

```bash
linear-cli comment list --issue-id ENG-123 --limit 20
linear-cli comment save --issue-id ENG-123 --body "Repro steps attached"
linear-cli comment save --id <comment-id> --body "Edited"
linear-cli comment save --issue-id ENG-123 --parent-id <comment-id> --body "Reply"
linear-cli comment delete --id <comment-id>
```

`comment save` maps to `save_comment`: pass `--id` to update, `--issue-id` (plus optional `--parent-id`) to create. `--body` takes literal Markdown.

### Attachments

```bash
linear-cli attachment get --id <attachment-id>
linear-cli attachment create --issue ENG-123 --file ./screenshot.png --title "Login error"
linear-cli attachment create --issue ENG-123 --base64 <content> --filename trace.log --content-type text/plain
linear-cli attachment delete --id <attachment-id>
```

`attachment create` wraps `create_attachment`. The preferred flow is `--file <path>`: the CLI reads the file, base64-encodes it, and infers a sensible `--filename` and `--content-type` from the extension. Override either with the matching flag. The `--base64` path is available for callers that already have encoded bytes.

### Cycles

```bash
linear-cli cycle list --team-id <team-uuid>
linear-cli cycle list --team-id <team-uuid> --type current
```

`cycle list` requires a team UUID (not a name or key) because that is how the underlying `list_cycles` tool is scoped. `--type` accepts `current`, `previous`, or `next`.

### Milestones

```bash
linear-cli milestone list --project "Q2 Reliability"
linear-cli milestone get --project "Q2 Reliability" --query "Design Review"
linear-cli milestone save --project "Q2 Reliability" --name "Design Review" --target-date 2026-05-15
linear-cli milestone save --project "Q2 Reliability" --id <milestone-id> --description "Updated"
linear-cli milestone save --project "Q2 Reliability" --id <milestone-id> --clear-target-date
```

Milestones are always scoped to a project. Creating requires `--project` and `--name`. Updating requires `--project` and `--id` (milestone name or ID). Use `--clear-target-date` to drop a target date explicitly.

### Documents

```bash
linear-cli document list --query "runbook" --project-id <uuid>
linear-cli document get --id <document-id-or-slug>
linear-cli document create --title "Runbook: auth outage" --content "# Steps\n\n1. ..." --project "Reliability"
linear-cli document update --id <document-id> --title "Runbook: auth outage (v2)"
```

`document create` attaches the new document either to a project (`--project`) or to an issue (`--issue`), never both. `document update` cannot re-scope a document to an issue; it only accepts project reassignment plus content and styling changes. `--content` takes literal Markdown.

### Teams

```bash
linear-cli team list --query eng --limit 10
linear-cli team get --query ENG
```

`team get` accepts a UUID, key (e.g. `ENG`), or name.

### Users

```bash
linear-cli user list --query albert --team ENG
linear-cli user get --query me
linear-cli user get --query alice@example.com
```

`user get` resolves by user ID, display name, email, or the literal `me`.

### Images

```bash
linear-cli image extract --markdown "See ![diagram](https://...) for details"
linear-cli image extract --from-file ./issue-description.md
```

`image extract` wraps `extract_images`. It fetches the referenced images through the authenticated session and returns them as structured content. The tool may emit non-text content items; the CLI passes them through unchanged.

### Documentation search

```bash
linear-cli docs search --query "api authentication"
linear-cli docs search --query "cycles" --page 2
```

Queries the Linear help docs through `search_documentation` and returns the ranked results as an array under `results`.

## Output

All command results are JSON-first. Success responses include `ok: true` and a `command` name. Errors include `ok: false` and an `error` object with a message, optional code, and optional details.

Tool-level errors that the MCP server returns as content (for example, "Entity not found") surface with `ok: true` and the server's message under `result`. Treat `ok: true` as "the CLI reached the server"; always inspect the body for domain-level errors.

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

The CLI wraps every tool exposed by the authenticated Linear MCP server as of the `2024-11-05` protocol. The mapping is:

| CLI | MCP tool |
| --- | --- |
| `project list` | `list_projects` |
| `project get` | `get_project` |
| `project save` | `save_project` |
| `project-label list` | `list_project_labels` |
| `issue list` | `list_issues` |
| `issue get` | `get_issue` |
| `issue save` | `save_issue` |
| `issue-label list` | `list_issue_labels` |
| `issue-label create` | `create_issue_label` |
| `status list` | `list_issue_statuses` |
| `status get` | `get_issue_status` |
| `comment list` | `list_comments` |
| `comment save` | `save_comment` |
| `comment delete` | `delete_comment` |
| `attachment get` | `get_attachment` |
| `attachment create` | `create_attachment` |
| `attachment delete` | `delete_attachment` |
| `cycle list` | `list_cycles` |
| `document list` | `list_documents` |
| `document get` | `get_document` |
| `document create` | `create_document` |
| `document update` | `update_document` |
| `milestone list` | `list_milestones` |
| `milestone get` | `get_milestone` |
| `milestone save` | `save_milestone` |
| `team list` | `list_teams` |
| `team get` | `get_team` |
| `user list` | `list_users` |
| `user get` | `get_user` |
| `image extract` | `extract_images` |
| `docs search` | `search_documentation` |

Null-clear flags available on write commands:
- `issue save`: `--clear-assignee`, `--clear-delegate`, `--clear-parent`, `--clear-duplicate-of`
- `project save`: `--clear-lead`
- `milestone save`: `--clear-target-date`

Each `--clear-*` flag is mutually exclusive with its matching value flag and routes a literal `null` through the MCP call.

Known gaps:
- `image extract` returns binary image content that the JSON-first formatter cannot render inline; the raw MCP payload is surfaced under `result` for downstream tools to decode.
- Linear's MCP surface has no `delete_issue` or `archive_issue` tool, so this CLI cannot hard-delete issues. Move spent test issues to a Canceled workflow state instead.
- Linear's MCP surface has no link-removal tool. Links added via `issue save --link` become attachments; remove them via `attachment delete --id <attachment-id>`.

## End-to-end regression test

`cli/scripts/e2e.sh` runs the full skill workflow against a real workspace (create issue, assign and clear assignee, add and delete a comment, attach and delete a file, filter by `--unassigned`, cancel the issue). Run it before each release to catch regressions like silent nullability changes that unit tests miss.

```bash
cd cli
LINEAR_DEFAULT_TEAM=<team> npm run e2e
# or: bash cli/scripts/e2e.sh --team <team>
# add --keep to skip the Canceled state cleanup when debugging a failure
```

The script writes only to a disposable test issue clearly titled "linear-cli e2e ... (safe to delete)". Linear's MCP has no `delete_issue` tool, so the issue is left in the Canceled state at the end.

## Skill file

Agent guidance lives in `skill/linear/SKILL.md`.
