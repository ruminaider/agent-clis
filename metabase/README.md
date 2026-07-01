# Metabase CLI

Drive Metabase from the terminal with full read/write: explore databases and tables, run SQL, execute and manage saved questions and dashboards, organize collections, search, and walk revision history. It mirrors the `@ruminaider/metabase-mcp-server` surface (46 operations) and shares its environment variables, so it is a drop-in swap for that MCP, with output optimized to stay cheap for agents to consume.

```bash
metabase-cli auth login --url https://analytics.example.com --api-key <key>
metabase-cli database list
metabase-cli query run 2 "select count(*) from orders where created_at > now() - interval '7 days'"
metabase-cli search "revenue" --models card,dashboard
```

## Quick start

```bash
bash metabase/install.sh          # or: npm i -g @ruminaider/metabase-cli
metabase-cli auth login --url https://your-instance.metabaseapp.com --api-key <key>
metabase-cli auth status
```

Create an API key in Metabase under Admin > Settings > Authentication > API Keys. Credentials persist to `~/.config/metabase-cli/credentials.json` (mode `0600`). Requires Node.js 18+.

## Authentication

Three methods, tried in priority order, matching the MCP:

| Priority | Method | Provide via |
|----------|--------|-------------|
| 1 | API key (recommended) | `--api-key` or `METABASE_API_KEY` |
| 2 | Session token | `--session-token` or `METABASE_SESSION_TOKEN` |
| 3 | Email + password | `--email`/`--password` or `METABASE_USER_EMAIL`/`METABASE_PASSWORD` |

`METABASE_URL` sets the instance. Environment variables override persisted config, so the same environment that ran the MCP works here unchanged. Pass `--read-only` (or `METABASE_READ_ONLY=true`) to block every write operation.

## Usage

All output is response-optimized JSON. Explore, then query, then act.

```bash
# Schema exploration
metabase-cli database list
metabase-cli database metadata 2
metabase-cli table get 45 46 47            # batch fetch
metabase-cli field values 123

# Queries
metabase-cli query run 2 "select * from users limit 10"
metabase-cli query export 2 "select * from events" --format csv --output events.csv

# Cards (saved questions) and dashboards
metabase-cli card list --f mine
metabase-cli card execute 88 --parameters '[{"type":"category","target":["variable",["template-tag","status"]],"value":"active"}]'
metabase-cli card create --name "Weekly signups" --display line --dataset-query '{"database":2,"type":"native","native":{"query":"select ..."}}'
metabase-cli dashboard get 12

# Collections, search, history
metabase-cli collection items root --models card,dashboard
metabase-cli search "churn" --models card
metabase-cli revision list card 88
metabase-cli bookmark add dashboard 12
```

Run `metabase-cli help` for the full command list. Every create/update/archive/copy/revert/bookmark/cache operation is blocked under `--read-only`.

## Architecture

- `cli/bin/metabase.js` parses arguments and dispatches all 46 operations; output is optimized JSON.
- `cli/lib/client.js` is the HTTP client: API-key / session-token / email-password auth, exponential-backoff retry, error classification, and 401 session auto-refresh.
- `cli/lib/api.js` maps each operation to its Metabase REST endpoint.
- `cli/lib/response.js` strips metadata bloat and flattens query results (`{rows,cols}` into objects), ported from the MCP.
- `cli/lib/auth.js` resolves credentials (options, then environment, then persisted config) and stores them.

Differences from the long-running MCP: the CLI is one short-lived process per command, so the MCP's in-memory schema cache does not apply (`METABASE_CACHE_TTL_MS` is a no-op), and exports write to a file or stdout rather than returning inline.

## Agent skill

`skill/metabase/SKILL.md` follows the [Agent Skills standard](https://agentskills.io) and works with pi, Claude Code, and any compatible harness.
