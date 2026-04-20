#!/usr/bin/env node

import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { clearCredentials, getAuthStatus, login, logout } from "../lib/auth.js";
import { listComments, listProjects, getProject } from "../lib/api.js";
import { initializeMcpSession, listTools } from "../lib/mcp.js";
import { CLI_NAME, CONFIG_ENV_KEYS, resolveConfigDefaults } from "../lib/config.js";

const COMMANDS = new Set([
  "auth",
  "mcp",
  "project",
  "comment",
  "help",
  "--help",
  "-h",
]);

const VALUE_FLAGS = new Set([
  "--api-key",
  "--team",
  "--workspace",
  "--project-id",
  "--issue-id",
  "--cursor",
  "--limit",
  "--order-by",
]);

export function printHelp() {
  console.log(`${CLI_NAME}

Usage:
  ${CLI_NAME} <command> [options]

Commands:
  auth login
  auth logout
  auth status
  mcp discover
  project list
  project get --project-id <project-id>
  comment list --issue-id <issue-id>

Global flags:
  --api-key <key>     Use a Linear API key for this invocation
  --team <team>       Override the default team
  --workspace <ws>    Override the default workspace

List and discovery flags:
  --cursor <cursor>
  --limit <n>
  --order-by <field>

Notes:
  - Output is JSON-first. Command results are printed as JSON.
  - Explicit flags are preferred for identifiers and context.
  - Current implementation covers only auth, mcp discover, project read, and comment list.
  - Future expansion remains intentionally out of scope until tool inventory is confirmed.

Environment overrides:
  ${CONFIG_ENV_KEYS.apiKey}
  ${CONFIG_ENV_KEYS.defaultTeam}
  ${CONFIG_ENV_KEYS.defaultWorkspace}`);
}

function parseArgs(argv) {
  const positionals = [];
  const values = new Map();

  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (!arg.startsWith("-") || arg === "-") {
      positionals.push(arg);
      continue;
    }

    const eqIndex = arg.indexOf("=");
    if (eqIndex !== -1) {
      const flag = arg.slice(0, eqIndex);
      const value = arg.slice(eqIndex + 1);
      if (!VALUE_FLAGS.has(flag)) throw new Error(`Unknown flag: ${flag}`);
      values.set(flag, value);
      continue;
    }

    if (arg === "-h" || arg === "--help") {
      values.set(arg, true);
      continue;
    }

    if (!VALUE_FLAGS.has(arg)) throw new Error(`Unknown flag: ${arg}`);
    const value = argv[i + 1];
    if (value === undefined || value.startsWith("-")) throw new Error(`Flag ${arg} requires a value`);
    values.set(arg, value);
    i++;
  }

  return { positionals, values };
}

function json(data) {
  console.log(JSON.stringify(data, null, 2));
}

function getFlag(parsed, name) {
  return parsed.values.get(name) ?? null;
}

function asInteger(value) {
  if (value === null) return null;
  const parsed = Number.parseInt(value, 10);
  if (!Number.isFinite(parsed) || String(parsed) !== String(Number.parseInt(String(value), 10))) return null;
  return parsed;
}

export async function main(argv = process.argv.slice(2)) {
  const parsed = parseArgs(argv);
  const [command, subcommand] = parsed.positionals;

  if (!command || parsed.values.has("--help") || parsed.values.has("-h") || command === "help") {
    printHelp();
    return 0;
  }

  const resolvedDefaults = await resolveConfigDefaults({
    defaultTeam: getFlag(parsed, "--team"),
    defaultWorkspace: getFlag(parsed, "--workspace"),
  });

  const commonOptions = {
    apiKey: getFlag(parsed, "--api-key"),
    team: resolvedDefaults.defaultTeam,
    workspace: resolvedDefaults.defaultWorkspace,
  };

  if (commonOptions.apiKey) {
    process.env.LINEAR_API_KEY = commonOptions.apiKey;
  }

  try {
    switch (command) {
      case "auth": {
        switch (subcommand) {
          case "login": {
            const credentials = await login({ apiKey: commonOptions.apiKey });
            json({ ok: true, command: "auth login", authenticated: true, authType: credentials?.auth_type ?? null });
            return 0;
          }
          case "logout": {
            await logout();
            await clearCredentials();
            json({ ok: true, command: "auth logout", loggedOut: true });
            return 0;
          }
          case "status": {
            const status = await getAuthStatus({ apiKey: commonOptions.apiKey });
            json({ ok: true, command: "auth status", ...status });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli auth <login|logout|status>");
        }
      }
      case "mcp": {
        if (subcommand !== "discover") throw new Error("Usage: linear-cli mcp discover");
        const session = await initializeMcpSession();
        const tools = await listTools();
        json({ ok: true, command: "mcp discover", session, tools });
        return 0;
      }
      case "project": {
        switch (subcommand) {
          case "list": {
            const result = await listProjects({
              apiKey: commonOptions.apiKey,
              team: commonOptions.team,
              workspace: commonOptions.workspace,
              cursor: getFlag(parsed, "--cursor"),
              limit: asInteger(getFlag(parsed, "--limit")),
              orderBy: getFlag(parsed, "--order-by"),
            });
            json({ ok: true, command: "project list", projects: result });
            return 0;
          }
          case "get": {
            const projectId = getFlag(parsed, "--project-id");
            if (!projectId) throw new Error("Usage: linear-cli project get --project-id <project-id>");
            const project = await getProject(projectId);
            json({ ok: true, command: "project get", project });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli project <list|get>");
        }
      }
      case "comment": {
        if (subcommand !== "list") throw new Error("Usage: linear-cli comment list --issue-id <issue-id>");
        const issueId = getFlag(parsed, "--issue-id");
        if (!issueId) throw new Error("Usage: linear-cli comment list --issue-id <issue-id>");
        const comments = await listComments(issueId, {
          cursor: getFlag(parsed, "--cursor"),
          limit: asInteger(getFlag(parsed, "--limit")),
          orderBy: getFlag(parsed, "--order-by"),
        });
        json({ ok: true, command: "comment list", comments });
        return 0;
      }
      default:
        throw new Error(`Unknown command: ${command}`);
    }
  } catch (error) {
    json({ ok: false, error: { message: error.message, code: error.code ?? null, details: error.details ?? null } });
    return 1;
  }
}

function isDirectExecution() {
  if (!process.argv[1]) return false;

  try {
    return realpathSync(process.argv[1]) === realpathSync(fileURLToPath(import.meta.url));
  } catch {
    return false;
  }
}

if (isDirectExecution()) {
  main()
    .then((code) => process.exit(code))
    .catch((error) => {
      console.error(JSON.stringify({ ok: false, error: { message: error instanceof Error ? error.message : String(error) } }, null, 2));
      process.exit(1);
    });
}
