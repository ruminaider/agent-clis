# Linear CLI

Scaffold package for the Linear tool. Core behavior will be added in later waves.

## Install

```bash
# Via npm
npm i -g @ruminaider/linear-cli

# Or from repo clone
bash install.sh
```

## Reserved command namespaces

- `auth`
- `search`
- `issue`
- `project`
- `team`
- `label`
- `comment`
- `config`
- `mcp`

## Reserved module surface

- `cli/bin/linear.js`: executable entrypoint
- `cli/lib/auth.js`: credentials and login flow
- `cli/lib/mcp.js`: MCP transport helpers
- `cli/lib/api.js`: Linear domain helpers
- `cli/lib/config.js`: shared constants and paths

## Discovery

### Verified public metadata

| Item | Verified value |
| --- | --- |
| OAuth metadata entry point | `https://mcp.linear.app/.well-known/oauth-authorization-server` |
| Protected resource metadata entry point | `https://mcp.linear.app/.well-known/oauth-protected-resource` |
| Authorization endpoint | `https://mcp.linear.app/authorize` |
| Token endpoint | `https://mcp.linear.app/token` |
| Registration endpoint | `https://mcp.linear.app/register` |
| Revocation endpoint | `https://mcp.linear.app/token`, returned in `/.well-known/oauth-authorization-server` |
| Response types supported | `code` |
| Response modes supported | `query` |
| Grant types supported | `authorization_code`, `refresh_token` |
| Token endpoint auth methods supported | `client_secret_basic`, `client_secret_post`, `none` |
| Bearer methods supported | `header` |
| Protocol reference in docs | Authenticated remote MCP spec `2025-03-26` |
| Transport reference in docs | Streamable HTTP with OAuth 2.1 and dynamic client registration |
| SSE deprecation reference | Protocol version `2024-11-05`, `/sse` deprecated in favor of `/mcp` |
| Direct bearer support claim from docs | The docs FAQ says the server supports passing OAuth tokens and API keys directly in `Authorization: Bearer <yourtoken>` |

### Unauthenticated probe results

| Probe | Observed result |
| --- | --- |
| `GET https://mcp.linear.app/mcp` | `401 Unauthorized`, JSON body, `Content-Type: application/json` |
| `POST https://mcp.linear.app/mcp` with JSON-RPC `tools/list` | `401 Unauthorized`, JSON body, `Content-Type: application/json` |
| `POST https://mcp.linear.app/mcp` with JSON-RPC `initialize` | `401 Unauthorized`, JSON body, `Content-Type: application/json` |
| `OPTIONS https://mcp.linear.app/mcp` | `204 No Content`, no `Content-Type` header |
| CORS behavior on public probes | `Access-Control-Allow-Origin` echoed the request origin, `Access-Control-Allow-Headers: Authorization, *`, `Access-Control-Allow-Methods: *`, `Access-Control-Max-Age: 86400` |
| `WWW-Authenticate` challenge | `Bearer realm="OAuth", resource_metadata="https://mcp.linear.app/.well-known/oauth-protected-resource", error="invalid_token", error_description="Missing or invalid access token"` |
| Session header behavior | No `Mcp-Session-Id` header was observed in unauthenticated probes |

### Verified tool and schema clues from public docs

| Tool or capability | Verified detail | Status |
| --- | --- | --- |
| `list_comments` | Supports pagination via `cursor`, `limit`, and `orderBy` | Verified in changelog, not from an authenticated `tools/list` response |
| `list_projects` | Response includes a `trashed` field | Verified in changelog, not from an authenticated `tools/list` response |
| `get_project` | Response includes a `trashed` field | Verified in changelog, not from an authenticated `tools/list` response |
| Issue writes | Issues created through the MCP without a `stateId` default to the team’s default state, even when triage is enabled, if the user is a member of the team | Verified in changelog, write-path caveat |
| Issue property access | Issue properties such as labels, project, and status can be modified and queried by name rather than UUID | Verified in changelog |
| New MCP capabilities | Create and edit initiatives, initiative updates, project milestones, project updates, manage project labels, load images, and load Linear resources through URLs | Verified in changelog, exact tool names not yet enumerated publicly |
| Search and fetch tools | Public changelog references search and fetch tools, but the exact MCP schemas were not exposed without authentication | Unverified |

### Auth-blocked inventory notes

- The managed server rejects unauthenticated `tools/list` and `initialize` calls with `401`, so the real tool catalog and argument schemas could not be enumerated from a public probe.
- The docs confirm authenticated remote MCP and direct bearer support, but do not expose a public unauthenticated tool manifest.
- Exact request and response shapes for write tools, especially create and update operations, remain unverified until we can authenticate.

## MVP command surface

Keep the first implementation deliberately conservative. Ship the smallest set that is clearly supported by the public discovery evidence, then expand after authenticated tool inventory is confirmed.

### MVP, verified and low risk

- `linear-cli auth login`
- `linear-cli auth logout`
- `linear-cli auth status`
- `linear-cli mcp discover`
- `linear-cli project get <project-id>`
- `linear-cli project list [filters]`
- `linear-cli comment list <issue-id> [cursor|limit|orderBy]`

### Deferred until authenticated tool inventory is confirmed

- `linear-cli search <query>`
- `linear-cli issue get <id-or-key>`
- `linear-cli issue list [filters]`
- `issue create`
- `issue update`
- `project create`
- `project update`
- `comment create`
- initiative commands of any kind
- project label management commands of any kind
- any command that depends on the exact `tools/list` schema rather than public docs, including search until the authenticated schema is confirmed

### Implementation guardrails

- Prefer read-only flows first, especially metadata discovery and project and comment reads.
- Treat write operations as opt-in and gate them behind authenticated capability checks.
- Normalize all server calls through one transport helper so the MCP auth flow, bearer mode, and future session handling stay isolated from CLI command logic.
- Use config precedence in this order: explicit CLI flags, environment variables, then persisted config at `~/.config/linear-cli/config.json`.
- Planned environment overrides: `LINEAR_API_KEY`, `LINEAR_DEFAULT_TEAM`, `LINEAR_DEFAULT_WORKSPACE`.
