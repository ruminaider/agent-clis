#!/usr/bin/env node

import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { getAuthStatus, login, logout } from "../lib/auth.js";
import { getProject, listComments, listProjects, saveProject } from "../lib/api.js";
import { initializeMcpSession, listTools } from "../lib/mcp.js";
import { CLI_NAME, CONFIG_ENV_KEYS, resolveConfigDefaults } from "../lib/config.js";

const COMMANDS = new Set(["auth", "mcp", "project", "comment", "help", "--help", "-h"]);
const VALUE_FLAGS = new Set([
  "--api-key",
  "--team",
  "--query",
  "--issue-id",
  "--cursor",
  "--limit",
  "--order-by",
  "--id",
  "--name",
  "--icon",
  "--color",
  "--summary",
  "--description",
  "--state",
  "--start-date",
  "--start-date-resolution",
  "--target-date",
  "--target-date-resolution",
  "--priority",
  "--lead",
  "--created-at",
  "--updated-at",
  "--initiative",
  "--member",
  "--label",
  "--labels",
  "--add-team",
  "--remove-team",
  "--set-team",
  "--add-initiative",
  "--remove-initiative",
  "--set-initiative",
]);
const BOOLEAN_FLAGS = new Set(["--include-milestones", "--include-members", "--include-resources", "--include-archived"]);
const MULTI_VALUE_FLAGS = new Set([
  "--labels",
  "--add-team",
  "--remove-team",
  "--set-team",
  "--add-initiative",
  "--remove-initiative",
  "--set-initiative",
]);

function appendFlagValue(values, flag, value) {
  if (!MULTI_VALUE_FLAGS.has(flag)) {
    values.set(flag, value);
    return;
  }

  const current = values.get(flag);
  if (current === undefined) {
    values.set(flag, [value]);
    return;
  }
  if (Array.isArray(current)) {
    current.push(value);
    return;
  }
  values.set(flag, [current, value]);
}

function parseBooleanValue(value, flag) {
  if (value === null || value === undefined) return true;

  const normalized = String(value).trim().toLowerCase();
  if (["", "true", "1", "yes", "on"].includes(normalized)) return true;
  if (["false", "0", "no", "off"].includes(normalized)) return false;
  throw new Error(`Flag ${flag} expects a boolean value`);
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
      if (BOOLEAN_FLAGS.has(flag)) {
        appendFlagValue(values, flag, parseBooleanValue(value, flag));
        continue;
      }
      if (!VALUE_FLAGS.has(flag)) throw new Error(`Unknown flag: ${flag}`);
      appendFlagValue(values, flag, value);
      continue;
    }

    if (arg === "-h" || arg === "--help") {
      values.set(arg, true);
      continue;
    }

    if (BOOLEAN_FLAGS.has(arg)) {
      const next = argv[i + 1];
      if (next !== undefined && !next.startsWith("-")) {
        appendFlagValue(values, arg, parseBooleanValue(next, arg));
        i += 1;
      } else {
        appendFlagValue(values, arg, true);
      }
      continue;
    }

    if (!VALUE_FLAGS.has(arg)) throw new Error(`Unknown flag: ${arg}`);
    const value = argv[i + 1];
    if (value === undefined || value.startsWith("-")) throw new Error(`Flag ${arg} requires a value`);
    appendFlagValue(values, arg, value);
    i += 1;
  }

  return { positionals, values };
}

function json(data) {
  console.log(JSON.stringify(data, null, 2));
}

function getFlag(parsed, name) {
  return parsed.values.get(name) ?? null;
}

function getFlagList(parsed, name) {
  const value = parsed.values.get(name);
  if (value === undefined || value === null || value === "") return null;
  return Array.isArray(value) ? value : [value];
}

function getBooleanFlag(parsed, name) {
  const value = parsed.values.get(name);
  return value === undefined ? null : Boolean(value);
}

function asInteger(value, { flag = null, min = null, max = null } = {}) {
  if (value === null) return null;
  const text = String(value).trim();
  if (!/^[-+]?\d+$/.test(text)) {
    throw new Error(`Flag ${flag ?? "<value>"} expects an integer`);
  }

  const parsed = Number.parseInt(text, 10);
  if (min !== null && parsed < min) {
    throw new Error(`Flag ${flag ?? "<value>"} must be at least ${min}`);
  }
  if (max !== null && parsed > max) {
    throw new Error(`Flag ${flag ?? "<value>"} must be at most ${max}`);
  }
  return parsed;
}

function assertChoice(flag, value, allowedValues) {
  if (value === null) return null;
  if (!allowedValues.includes(value)) {
    throw new Error(`Flag ${flag} must be one of: ${allowedValues.join(", ")}`);
  }
  return value;
}

function printHelp() {
  console.log(`${CLI_NAME}

Usage:
  ${CLI_NAME} <command> [options]

Commands:
  auth login
  auth logout
  auth status
  mcp discover
  project list
  project get --query <query>
  project save
  comment list --issue-id <issue-id>

Project list flags:
  --team <team>                 Filter by team name or ID
  --query <query>               Search project name
  --state <state>               Filter by project state
  --initiative <initiative>     Filter by initiative name or ID
  --member <member>             Filter by member user ID, name, email, or "me"
  --label <label>               Filter by a single label
  --cursor <cursor>
  --limit <n>
  --order-by createdAt|updatedAt
  --created-at <iso-date-or-duration>
  --updated-at <iso-date-or-duration>
  --include-milestones
  --include-members
  --include-archived

Project get flags:
  --query <query>               Project name, ID, or slug
  --include-milestones
  --include-members
  --include-resources

Project save flags:
  --id <project-id>             Required when updating an existing project
  --name <name>                 Required when creating a project
  --icon <icon>
  --color <color>
  --summary <summary>
  --description <markdown>
  --state <state>
  --start-date <iso-date>
  --start-date-resolution halfYear|month|quarter|year
  --target-date <iso-date>
  --target-date-resolution halfYear|month|quarter|year
  --priority <0-4>
  --lead <user>
  --add-team <team>             Repeatable when creating or expanding team membership
  --remove-team <team>          Repeatable
  --set-team <team>             Repeatable, replaces the team set
  --labels <label>              Repeatable project labels
  --add-initiative <initiative> Repeatable
  --remove-initiative <initiative> Repeatable
  --set-initiative <initiative> Repeatable, replaces the initiative set

Comment list flags:
  --issue-id <issue-id>
  --cursor <cursor>
  --limit <n>
  --order-by createdAt|updatedAt

Notes:
  - Output is JSON-first. Command results are printed as JSON.
  - Project lookups use the authenticated MCP schema directly, including project get(query).
  - Comment listing uses issueId exactly as exposed by the server, and the issue identifier can be an ID or key.
  - project save is the only shipped write command. It maps 1:1 to save_project and is intended for verified live checks only.
  - Creating a project requires --name plus at least one team assignment flag.
  - The CLI negotiates MCP protocol 2024-11-05 first, with fallback only if the server rejects it.

Environment overrides:
  ${CONFIG_ENV_KEYS.apiKey}
  ${CONFIG_ENV_KEYS.defaultTeam}`);
}

function normalizeProjectSaveInput(parsed) {
  return {
    id: getFlag(parsed, "--id"),
    name: getFlag(parsed, "--name"),
    icon: getFlag(parsed, "--icon"),
    color: getFlag(parsed, "--color"),
    summary: getFlag(parsed, "--summary"),
    description: getFlag(parsed, "--description"),
    state: getFlag(parsed, "--state"),
    startDate: getFlag(parsed, "--start-date"),
    startDateResolution: assertChoice("--start-date-resolution", getFlag(parsed, "--start-date-resolution"), ["halfYear", "month", "quarter", "year"]),
    targetDate: getFlag(parsed, "--target-date"),
    targetDateResolution: assertChoice("--target-date-resolution", getFlag(parsed, "--target-date-resolution"), ["halfYear", "month", "quarter", "year"]),
    priority: asInteger(getFlag(parsed, "--priority"), { flag: "--priority", min: 0, max: 4 }),
    lead: getFlag(parsed, "--lead"),
    addTeams: getFlagList(parsed, "--add-team"),
    removeTeams: getFlagList(parsed, "--remove-team"),
    setTeams: getFlagList(parsed, "--set-team"),
    labels: getFlagList(parsed, "--labels"),
    addInitiatives: getFlagList(parsed, "--add-initiative"),
    removeInitiatives: getFlagList(parsed, "--remove-initiative"),
    setInitiatives: getFlagList(parsed, "--set-initiative"),
  };
}

function requiresProjectCreateTeamFlags(parsed) {
  return getFlag(parsed, "--name") && !getFlag(parsed, "--id") && !getFlagList(parsed, "--add-team") && !getFlagList(parsed, "--set-team");
}

export async function main(argv = process.argv.slice(2)) {
  const parsed = parseArgs(argv);
  const [command, subcommand] = parsed.positionals;

  if (!command || parsed.values.has("--help") || parsed.values.has("-h") || command === "help") {
    printHelp();
    return 0;
  }

  if (!COMMANDS.has(command)) {
    json({ ok: false, error: { message: `Unknown command: ${command}`, code: null, details: null } });
    return 1;
  }

  const apiKey = getFlag(parsed, "--api-key");
  if (apiKey) {
    process.env.LINEAR_API_KEY = apiKey;
  }

  try {
    switch (command) {
      case "auth": {
        switch (subcommand) {
          case "login": {
            const credentials = await login({ apiKey });
            json({ ok: true, command: "auth login", authenticated: true, authType: credentials?.auth_type ?? null });
            return 0;
          }
          case "logout": {
            await logout();
            json({ ok: true, command: "auth logout", loggedOut: true });
            return 0;
          }
          case "status": {
            const status = await getAuthStatus({ apiKey });
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
            const defaults = await resolveConfigDefaults({ defaultTeam: getFlag(parsed, "--team") });
            const result = await listProjects({
              team: defaults.defaultTeam,
              query: getFlag(parsed, "--query"),
              state: getFlag(parsed, "--state"),
              initiative: getFlag(parsed, "--initiative"),
              member: getFlag(parsed, "--member"),
              label: getFlag(parsed, "--label"),
              createdAt: getFlag(parsed, "--created-at"),
              updatedAt: getFlag(parsed, "--updated-at"),
              includeMilestones: getBooleanFlag(parsed, "--include-milestones"),
              includeMembers: getBooleanFlag(parsed, "--include-members"),
              includeArchived: getBooleanFlag(parsed, "--include-archived"),
              cursor: getFlag(parsed, "--cursor"),
              limit: asInteger(getFlag(parsed, "--limit"), { flag: "--limit", min: 1, max: 250 }),
              orderBy: assertChoice("--order-by", getFlag(parsed, "--order-by"), ["createdAt", "updatedAt"]),
            });
            json({ ok: true, command: "project list", ...result });
            return 0;
          }
          case "get": {
            const query = getFlag(parsed, "--query");
            if (!query) throw new Error("Usage: linear-cli project get --query <query>");
            const project = await getProject(query, {
              includeMilestones: getBooleanFlag(parsed, "--include-milestones"),
              includeMembers: getBooleanFlag(parsed, "--include-members"),
              includeResources: getBooleanFlag(parsed, "--include-resources"),
            });
            json({ ok: true, command: "project get", project });
            return 0;
          }
          case "save": {
            const input = normalizeProjectSaveInput(parsed);
            if (!input.id && !input.name) {
              throw new Error("Usage: linear-cli project save --id <project-id> [fields] or linear-cli project save --name <name> --add-team <team>");
            }
            if (requiresProjectCreateTeamFlags(parsed)) {
              throw new Error("Creating a project requires at least one --add-team or --set-team flag");
            }
            const project = await saveProject(input);
            json({ ok: true, command: "project save", project });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli project <list|get|save>");
        }
      }
      case "comment": {
        if (subcommand !== "list") throw new Error("Usage: linear-cli comment list --issue-id <issue-id>");
        const issueId = getFlag(parsed, "--issue-id");
        if (!issueId) throw new Error("Usage: linear-cli comment list --issue-id <issue-id>");
        const comments = await listComments(issueId, {
          cursor: getFlag(parsed, "--cursor"),
          limit: asInteger(getFlag(parsed, "--limit"), { flag: "--limit", min: 1, max: 250 }),
          orderBy: assertChoice("--order-by", getFlag(parsed, "--order-by"), ["createdAt", "updatedAt"]),
        });
        json({ ok: true, command: "comment list", ...comments });
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
