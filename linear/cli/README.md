# @ruminaider/linear-cli

JSON-first CLI access to Linear through its authenticated remote MCP server.

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

## Notes

- Output is JSON-first.
- The current Linear MCP session negotiates protocol `2024-11-05`.
- `project save` is the only shipped write command.
- Broader Linear issue, team, and document flows remain intentionally out of scope for now.
