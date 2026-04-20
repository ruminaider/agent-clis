---
name: linear
description: Use when the user needs to search, read, create, or update Linear issues, projects, teams, labels, or comments from the terminal. This is a scaffold only, core behavior is not implemented yet.
compatibility: Requires Node.js. Run `linear-cli auth` once the implementation lands.
---

# Linear CLI

## Overview
`linear-cli` is the planned Linear companion CLI. This wave only reserves the package shape, export names, and command namespaces.

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

## Reserved exports
- `lib/auth.js`: `loadCredentials`, `saveCredentials`, `clearCredentials`, `refreshToken`, `login`, `getAccessToken`
- `lib/mcp.js`: `initializeMcpSession`, `resetMcpSession`, `callTool`, `listTools`
- `lib/api.js`: `searchIssues`, `getIssue`, `createIssue`, `updateIssue`, `archiveIssue`, `listProjects`, `getProject`, `listTeams`, `getTeam`, `listUsers`, `listLabels`, `listStates`, `addComment`
- `lib/config.js`: `TOOL_NAME`, `PACKAGE_NAME`, `CLI_NAME`, `PACKAGE_VERSION`, `COMMAND_NAMESPACES`, `CONFIG_DIR`, `CREDENTIALS_FILE`, `SETTINGS_FILE`, `CACHE_DIR`, `MCP_URL`, `API_BASE_URL`
