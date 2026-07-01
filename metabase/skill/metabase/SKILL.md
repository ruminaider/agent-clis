---
name: metabase
description: "Use when the user wants to work in Metabase through `metabase-cli`: authenticate, explore databases/tables/fields, run SQL, execute or manage saved questions (cards), dashboards, collections, search, and revision history. Also use when the user explicitly asks for `metabase-cli`. Do NOT use for unrelated BI tools or local files."
compatibility: Requires Node.js 18+ and a Metabase instance URL plus an API key (recommended) or email/password. Run `metabase-cli auth login --url <url> --api-key <key>`.
---

# Metabase CLI

## Overview
`metabase-cli` gives terminal access to a Metabase instance with full read/write across cards (saved questions), dashboards, databases, tables, fields, queries, collections, search, and revision history. It mirrors the `metabase-mcp-server` surface (46 operations) and shares its environment variables, so it is a drop-in swap for that MCP. Output is response-optimized JSON that strips metadata bloat and flattens query results, keeping it cheap for agents to consume.

## Core Philosophy
**Explore before querying.** Resolve database, table, and field IDs with `database list`, `database metadata`, `table list`, and `search` before writing SQL or building cards. Metabase APIs take numeric IDs.

**Read before write.** Fetch a card, dashboard, or collection first, then update only the fields you intend to change. Update commands accept specific flags plus a generic `--set '<json>'` escape hatch for any field.

**Prefer read-only when browsing.** Pass `--read-only` (or set `METABASE_READ_ONLY=true`) to block every create/update/archive/copy/revert/bookmark/cache operation. Use it whenever writes are not intended.

**Use execute for small results, export for large.** `card execute` and `query run` cap at ~2000 rows; `card export` and `query export` stream CSV/JSON/XLSX up to 1M rows to a file.

## Domain Mechanics
1. **Auth.** `metabase-cli auth login --url <url> --api-key <key>` verifies against `/api/user/current` and persists to `~/.config/metabase-cli/credentials.json`. `auth status` checks it; `auth logout` clears it. Env vars override persisted config: `METABASE_URL`, `METABASE_API_KEY`, `METABASE_SESSION_TOKEN`, `METABASE_USER_EMAIL` + `METABASE_PASSWORD`. API key is recommended (create one in Admin > Settings > Authentication > API Keys).
2. **Databases & schema.** `database list`, `database get <id>`, `database metadata <id>` (all tables/fields/FKs), `database schemas <id>`, `database schema-tables <id> <schema>`.
3. **Tables & fields.** `table list`, `table get <id...>` (batch), `table metadata <id>`, `table fks <id>`; `field get <id>`, `field values <id>`, `field update <id> [--display-name --description --semantic-type --visibility-type]`.
4. **Queries.** `query run <db-id> "<sql>"` (native SQL, flattened rows), `query export <db-id> "<sql>" --format csv|json|xlsx [--output path]`, `query to-native --mbql '<json>'` (MBQL to SQL).
5. **Cards (saved questions).** `card list [--f all|archived|mine|popular|recent]`, `card get <id...>` (batch), `card create --name --display --dataset-query '<json>'`, `card update <id> [flags|--set json]`, `card copy <id>`, `card execute <id> [--parameters json]`, `card export <id> --format ...`, `card metadata <id>`, `card dashboards <id>`, `card archive <id>`.
6. **Dashboards.** `dashboard list|get <id...>|create|update <id>|copy <id>|archive <id>|metadata <id>`, and `dashboard set-cards <id> --cards '<json>'` to set the full card layout (add, remove, reposition, resize).
7. **Collections.** `collection list|tree|create|update <id>`, `collection get <id|root>`, `collection items <id|root> [--models card,dashboard --limit --offset]`.
8. **Discovery & history.** `search [<q>] [--models card,dashboard,table,...]`, `recent`, `whoami`, `cache invalidate [--database N|--dashboard N]`, `revision list <card|dashboard> <id>`, `revision revert <card|dashboard> <id> <revision-id>`, `bookmark add|remove <card|dashboard|collection> <id>`.

*Judgment:* When a name is given but not an ID, `search` first to resolve it. For writes, do a read-then-write cycle. JSON-valued flags (`--dataset-query`, `--parameters`, `--cards`, `--visualization-settings`, `--set`, `--mbql`) take a JSON string.

## Common Mistakes
- Passing a name where a numeric ID is required. Resolve it with `search` or a `list` first.
- Expecting `query run` to return every row. It caps at ~2000; use `query export --output file` for full extracts.
- Trying to write `xlsx` to stdout. Binary exports require `--output <path.xlsx>`.
- Forgetting `--read-only` when only browsing, then making an accidental write.
- Treating the in-memory cache as present. Unlike the MCP, the CLI is one short-lived process per command, so `METABASE_CACHE_TTL_MS` is a no-op.
- Passing raw MBQL to `query run` (it takes native SQL); use `query to-native` to convert MBQL first.
