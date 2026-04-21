# Implementation Plan

## Goal
Deliver a new `linear-cli` package and `linear` pi skill in the monorepo that wrap Linear's official remote MCP server with OAuth-backed terminal workflows for issues, projects, and comments.

## Tasks
1. **Phase 0, confirm the Linear MCP contract and freeze the MVP command surface**: Authenticate against `https://mcp.linear.app/mcp`, run `tools/list`, and map real tool names and schemas to a small initial CLI surface before writing wrappers.
   - File: `linear/README.md`
   - Changes: document only the verified MVP commands and any server caveats discovered during tool inventory. Prefer issue, comment, and project operations that are both listed and callable.
   - Acceptance: every planned CLI command maps to a verified Linear MCP tool, and the docs do not rely on guessed names or unsupported mutations.

2. **Scaffold the new Linear package from the Notion layout**: Create the sibling `linear/` package with the same top-level structure used by `notion/`.
   - File: `package.json`
   - Changes: add `linear/cli` to the workspace list.
   - File: `README.md`
   - Changes: add Linear to the tool table and install overview.
   - File: `linear/README.md`
   - Changes: add package-level quick start, auth, commands, and examples.
   - File: `linear/install.sh`
   - Changes: add a standalone installer that mirrors `notion/install.sh` but installs `linear-cli`.
   - File: `linear/cli/package.json`
   - Changes: create the npm package metadata, `linear-cli` bin entry, repository path, keywords, and dependencies.
   - Acceptance: `npm install` at the monorepo root includes `linear/cli`, `bash linear/install.sh` links `linear-cli`, and `linear-cli --help` resolves to the new package.

3. **Implement Linear OAuth and credential handling**: Reuse the Notion auth pattern, but point it at Linear's remote MCP auth endpoints and credential path.
   - File: `linear/cli/lib/auth.js`
   - Changes: implement dynamic client registration, PKCE, browser login, token refresh, logout, and status output for `https://mcp.linear.app/{register,authorize,token}`. Store credentials in `~/.config/linear-cli/credentials.json`.
   - File: `linear/cli/bin/linear.js`
   - Changes: expose `auth`, `auth status`, and `auth logout` commands with the same UX pattern as `notion-cli`.
   - Acceptance: login opens the browser, stores credentials, `linear-cli auth status` reports token state, refresh happens automatically, and logout clears the credential file.

4. **Build the Linear MCP transport layer**: Adapt the Notion MCP client to Linear's streamable HTTP server and keep the debug surface small.
   - File: `linear/cli/lib/mcp.js`
   - Changes: target `https://mcp.linear.app/mcp`, negotiate protocol versions dynamically, prefer the live server's current `2024-11-05` protocol, preserve session reuse within a single CLI invocation, support capability discovery, and parse both SSE and JSON responses if Linear varies by content type.
   - File: `linear/cli/bin/linear.js`
   - Changes: add an `mcp discover` command for capability discovery and troubleshooting.
   - Acceptance: authenticated `linear-cli mcp discover` returns server capabilities or tool descriptions, repeated calls in one invocation reuse the session ID, and server-side errors are surfaced clearly.

5. **Implement thin API wrappers and the MVP CLI commands**: Keep the command set intentionally small and aligned to verified tool support.
   - File: `linear/cli/lib/api.js`
   - Changes: add one wrapper per verified MCP tool. The shipped surface now covers every Linear MCP tool: `project list/get/save`, `project-label list`, `issue list/get/save`, `issue-label list/create`, `status list/get`, `comment list/save/delete`, `attachment get/create/delete`, `cycle list`, `document list/get/create/update`, `milestone list/get/save`, `team list/get`, `user list/get`, `image extract`, and `docs search`.
   - File: `linear/cli/bin/linear.js`
   - Changes: add the top-level parser, subcommands, flags, help text, and JSON output behavior. Follow the `notion-cli` style of explicit flags and minimal formatting.
   - File: `linear/README.md`
   - Changes: document the final CLI syntax and example commands.
   - Acceptance: each CLI command maps 1:1 to a working MCP tool, help text names all required flags, and manual smoke tests succeed for at least one read path and one safe reversible write path in a disposable or otherwise safe Linear target.

6. **Write the pi skill with Linear-specific operating guidance**: Mirror the notion skill structure, but teach agents how to work safely with Linear entities.
   - File: `linear/skill/linear/SKILL.md`
   - Changes: add overview, auth instructions, when to use `linear-cli`, and judgment rules such as resolving exact issue IDs before mutation, checking valid statuses before state changes, and reading current issue or project context before editing.
   - File: `linear/README.md`
   - Changes: align user-facing docs with the skill's command surface and safety guidance.
   - Acceptance: the skill is discoverable beside the Notion skill, names the supported commands, and gives enough guidance for an agent to use `linear-cli` without guessing identifiers or status values.

7. **Run end-to-end packaging and workflow verification**: Validate install, auth, read, write, and packaging from a clean environment.
   - File: `linear/cli/package.json`
   - Changes: finalize package metadata, published files, and any dependency cleanup.
   - File: `linear/install.sh`
   - Changes: make any fixes needed for local-link or global-install reliability.
   - File: `linear/README.md`
   - Changes: correct any command examples or caveats found during verification.
   - Acceptance: from a clean shell, `bash linear/install.sh` or `npm link` yields a working `linear-cli`; auth succeeds; `linear-cli mcp discover` works; at least one read and one write command succeed; the published package would exclude unnecessary files.

## Files to Modify
- `README.md` - add Linear to the monorepo tool list and install documentation.
- `package.json` - add `linear/cli` to the workspace configuration.
- `linear/README.md` - package docs for install, auth, commands, and examples.
- `linear/install.sh` - standalone installer for `linear-cli`.
- `linear/cli/package.json` - npm package metadata and bin registration.
- `linear/cli/bin/linear.js` - CLI argument parsing, help text, auth commands, and command dispatch.
- `linear/cli/lib/auth.js` - OAuth login, refresh, logout, and credential persistence.
- `linear/cli/lib/mcp.js` - remote MCP transport, initialization, tool calls, and tool discovery.
- `linear/cli/lib/api.js` - thin wrappers for verified Linear MCP tools.
- `linear/skill/linear/SKILL.md` - pi skill guidance and safe operating rules.

## New Files
- `linear/README.md` - package-level documentation.
- `linear/install.sh` - installer for the new tool.
- `linear/cli/package.json` - new npm package definition.
- `linear/cli/bin/linear.js` - executable CLI entrypoint.
- `linear/cli/lib/auth.js` - Linear OAuth implementation.
- `linear/cli/lib/mcp.js` - Linear remote MCP client.
- `linear/cli/lib/api.js` - Linear API wrapper layer.
- `linear/skill/linear/SKILL.md` - pi skill for the CLI.

## Dependencies
- Task 1 must finish before Task 5 and Task 6, because the real Linear tool inventory should define the supported command surface.
- Task 2 must finish before Tasks 3 through 7, because the package skeleton and workspace wiring need to exist first.
- Tasks 3 and 4 must finish before Task 5, because command wrappers depend on auth and MCP transport.
- Task 5 should finish before Task 6, because the skill and examples should describe the actual shipped commands.
- Task 7 depends on Tasks 2 through 6.

## Risks
- Linear's docs confirm remote MCP, OAuth 2.1, and dynamic client registration. The authenticated tool inventory is now known, but any future surface expansion should still be gated on real `mcp discover` results rather than assumptions.
- There is at least one public report of tool drift, where a tool may appear in discovery but fail at call time. Validate every new write-path tool before making it part of the documented CLI surface.
- The current Notion MCP client assumes SSE parsing. Linear documents streamable HTTP, so the transport should be tested against both SSE and plain JSON response shapes.
- Write-path verification needs a disposable Linear workspace, team, issue, or another clearly safe reversible target. Without that, mutation testing is incomplete.
- Linear documents direct `Authorization: Bearer` usage for API keys or external OAuth tokens, and the shipped CLI now supports both browser OAuth and explicit API key auth.
