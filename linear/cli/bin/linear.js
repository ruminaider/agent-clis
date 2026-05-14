#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { basename, extname } from "node:path";
import { realpathSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { getAuthStatus, login, logout } from "../lib/auth.js";
import {
  createAttachment,
  createDocument,
  createIssueLabel,
  deleteAttachment,
  deleteComment,
  extractImages,
  getAttachment,
  getDocument,
  getIssue,
  getIssueStatus,
  getMilestone,
  getProject,
  getTeam,
  getUser,
  listComments,
  listCycles,
  listDocuments,
  listIssueLabels,
  listIssueStatuses,
  listIssues,
  listMilestones,
  listProjectLabels,
  listProjects,
  listTeams,
  listUsers,
  saveComment,
  saveIssue,
  saveMilestone,
  saveProject,
  searchDocumentation,
  updateDocument,
} from "../lib/api.js";
import { initializeMcpSession, listTools } from "../lib/mcp.js";
import { CLI_NAME, CONFIG_ENV_KEYS, resolveConfigDefaults } from "../lib/config.js";

const COMMANDS = new Set([
  "auth",
  "mcp",
  "project",
  "project-label",
  "issue",
  "issue-label",
  "status",
  "comment",
  "attachment",
  "cycle",
  "document",
  "milestone",
  "team",
  "user",
  "image",
  "docs",
  "help",
  "--help",
  "-h",
]);

const VALUE_FLAGS = new Set([
  "--api-key",
  "--team",
  "--team-id",
  "--query",
  "--issue-id",
  "--issue",
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
  "--initiative-id",
  "--member",
  "--label",
  "--labels",
  "--add-team",
  "--remove-team",
  "--set-team",
  "--add-initiative",
  "--remove-initiative",
  "--set-initiative",
  "--title",
  "--subtitle",
  "--project",
  "--project-id",
  "--assignee",
  "--delegate",
  "--cycle",
  "--milestone",
  "--due-date",
  "--parent",
  "--parent-id",
  "--estimate",
  "--body",
  "--type",
  "--file",
  "--filename",
  "--content-type",
  "--base64",
  "--content",
  "--markdown",
  "--from-file",
  "--creator-id",
  "--link",
  "--blocks",
  "--blocked-by",
  "--related-to",
  "--duplicate-of",
  "--remove-blocks",
  "--remove-blocked-by",
  "--remove-related-to",
  "--page",
]);

const BOOLEAN_FLAGS = new Set([
  "--include-milestones",
  "--include-members",
  "--include-resources",
  "--include-archived",
  "--include-relations",
  "--include-customer-needs",
  "--is-group",
  "--clear-assignee",
  "--clear-delegate",
  "--clear-parent",
  "--clear-duplicate-of",
  "--clear-lead",
  "--clear-target-date",
  "--unassigned",
]);

const MULTI_VALUE_FLAGS = new Set([
  "--labels",
  "--add-team",
  "--remove-team",
  "--set-team",
  "--add-initiative",
  "--remove-initiative",
  "--set-initiative",
  "--link",
  "--blocks",
  "--blocked-by",
  "--related-to",
  "--remove-blocks",
  "--remove-blocked-by",
  "--remove-related-to",
]);

const CONTENT_TYPE_BY_EXT = {
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".gif": "image/gif",
  ".webp": "image/webp",
  ".svg": "image/svg+xml",
  ".pdf": "application/pdf",
  ".txt": "text/plain",
  ".md": "text/markdown",
  ".json": "application/json",
  ".csv": "text/csv",
  ".log": "text/plain",
  ".zip": "application/zip",
};

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

function output(command, result) {
  if (result && typeof result === "object" && !Array.isArray(result)) {
    json({ ok: true, command, ...result });
    return;
  }
  json({ ok: true, command, result });
}

function getFlag(parsed, name) {
  // Returns undefined for missing flags so api.js can distinguish "not provided"
  // (stripped before the MCP call) from an explicit null passed by a --clear-*
  // flag (kept, so the server sees the clear intent).
  return parsed.values.get(name);
}

function getFlagList(parsed, name) {
  const value = parsed.values.get(name);
  if (value === undefined || value === null || value === "") return null;
  return Array.isArray(value) ? value : [value];
}

function getBooleanFlag(parsed, name) {
  const value = parsed.values.get(name);
  return value === undefined ? undefined : Boolean(value);
}

function asInteger(value, { flag = null, min = null, max = null } = {}) {
  if (value === null || value === undefined) return undefined;
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
  if (value === null || value === undefined) return undefined;
  if (!allowedValues.includes(value)) {
    throw new Error(`Flag ${flag} must be one of: ${allowedValues.join(", ")}`);
  }
  return value;
}

function commonListOptions(parsed) {
  return {
    cursor: getFlag(parsed, "--cursor"),
    limit: asInteger(getFlag(parsed, "--limit"), { flag: "--limit", min: 1, max: 250 }),
    orderBy: assertChoice("--order-by", getFlag(parsed, "--order-by"), ["createdAt", "updatedAt"]),
  };
}

function inferContentType(filename) {
  const ext = extname(filename).toLowerCase();
  return CONTENT_TYPE_BY_EXT[ext] ?? "application/octet-stream";
}

async function resolveAttachmentPayload(parsed) {
  const filePath = getFlag(parsed, "--file");
  const base64 = getFlag(parsed, "--base64");
  const filenameFlag = getFlag(parsed, "--filename");
  const contentTypeFlag = getFlag(parsed, "--content-type");

  if (filePath) {
    const buffer = await readFile(filePath);
    return {
      base64Content: buffer.toString("base64"),
      filename: filenameFlag || basename(filePath),
      contentType: contentTypeFlag || inferContentType(filenameFlag || filePath),
    };
  }

  if (!base64) {
    throw new Error("attachment create requires --file <path> or --base64 <content> with --filename and --content-type");
  }
  if (!filenameFlag || !contentTypeFlag) {
    throw new Error("attachment create with --base64 also requires --filename and --content-type");
  }
  return { base64Content: base64, filename: filenameFlag, contentType: contentTypeFlag };
}

const COMMAND_HELP = {
  auth: {
    _overview: `Usage: ${CLI_NAME} auth <login|logout|status>

Subcommands:
  login [--api-key <key>]   OAuth flow, or persist an API key with --api-key
  logout                    Clear persisted credentials
  status [--api-key <key>]  Report active credential type and token state`,
    login: `Usage: ${CLI_NAME} auth login [--api-key <key>]

Starts the OAuth browser flow. Pass --api-key to persist a Linear API key instead.
Credentials persist in ~/.config/linear-cli/credentials.json.`,
    logout: `Usage: ${CLI_NAME} auth logout

Clears persisted credentials from ~/.config/linear-cli/credentials.json.`,
    status: `Usage: ${CLI_NAME} auth status [--api-key <key>]

Reports whether credentials are available and their type. Use --api-key to test
a candidate key without persisting it.`,
  },
  mcp: {
    _overview: `Usage: ${CLI_NAME} mcp discover

Returns negotiated protocol version, session info, server capabilities, and the
current tool inventory as JSON.`,
    discover: `Usage: ${CLI_NAME} mcp discover

Returns negotiated protocol version, session info, server capabilities, and the
current tool inventory as JSON.`,
  },
  project: {
    _overview: `Usage: ${CLI_NAME} project <list|get|save>`,
    list: `Usage: ${CLI_NAME} project list [filters]

Flags:
  --team <team>                 Team name or ID
  --query <query>               Search project name
  --state <state>               Filter by project state
  --initiative <name-or-id>
  --member <user|me>
  --label <label>
  --created-at <iso|duration>   e.g. -P7D
  --updated-at <iso|duration>
  --include-milestones          Include milestones
  --include-members             Include project members
  --include-archived            Include archived projects
  --cursor <cursor>
  --limit <n>                   1..250
  --order-by createdAt|updatedAt`,
    get: `Usage: ${CLI_NAME} project get --query <query> [flags]

Required:
  --query <name|id|slug>

Flags:
  --include-milestones
  --include-members
  --include-resources`,
    save: `Usage: ${CLI_NAME} project save [--id <id> | --name <name> --add-team <team>] [fields]

Create:
  Requires --name and at least one --add-team or --set-team.
Update:
  Requires --id.

Fields:
  --icon --color --summary --description --state --priority 0-4
  --start-date --start-date-resolution halfYear|month|quarter|year
  --target-date --target-date-resolution halfYear|month|quarter|year
  --lead <user>                   or --clear-lead (null-to-remove)
  --add-team --remove-team --set-team           (repeatable)
  --labels                                       (repeatable)
  --add-initiative --remove-initiative --set-initiative (repeatable)`,
  },
  "project-label": {
    _overview: `Usage: ${CLI_NAME} project-label list`,
    list: `Usage: ${CLI_NAME} project-label list [flags]

Flags:
  --name <filter>
  --cursor --limit --order-by createdAt|updatedAt`,
  },
  issue: {
    _overview: `Usage: ${CLI_NAME} issue <list|get|save>`,
    list: `Usage: ${CLI_NAME} issue list [filters]

Flags:
  --query --team --state --cycle --label --assignee --delegate --project
  --priority 0-4 --parent-id --created-at --updated-at --include-archived
  --cursor --limit --order-by createdAt|updatedAt
  --unassigned               Filter to issues with no assignee
                             (mutually exclusive with --assignee)`,
    get: `Usage: ${CLI_NAME} issue get --id <issue-id> [flags]

Required:
  --id <uuid-or-key>    e.g. ENG-123

Flags:
  --include-relations
  --include-customer-needs`,
    save: `Usage: ${CLI_NAME} issue save [--id <id> | --title <title> --team <team>] [fields]

Create:
  Requires --title and --team. Do NOT pass --id.
Update:
  Requires --id (UUID or key like ENG-123).

Core fields:
  --title --description (literal Markdown) --project --state --priority 0-4
  --cycle --milestone --labels (repeatable) --due-date --estimate
  --assignee <user>    or --clear-assignee
  --delegate <agent>   or --clear-delegate
  --parent <issue>     or --clear-parent

Relations (append-only):
  --link "url|title"   (repeatable)
  --blocks --blocked-by --related-to   (repeatable)
  --duplicate-of <issue>   or --clear-duplicate-of

Relation removal:
  --remove-blocks --remove-blocked-by --remove-related-to   (repeatable)

Notes:
  - Use --clear-* to pass literal null (Linear's "remove this value" semantics).
  - Links become attachments; remove them via 'attachment delete --id <attachment-id>'.`,
  },
  "issue-label": {
    _overview: `Usage: ${CLI_NAME} issue-label <list|create>`,
    list: `Usage: ${CLI_NAME} issue-label list [flags]

Flags:
  --name <filter> --team <team>
  --cursor --limit --order-by createdAt|updatedAt`,
    create: `Usage: ${CLI_NAME} issue-label create --name <name> [flags]

Required:
  --name <name>

Flags:
  --description <text> --color <hex>
  --team-id <team-uuid>   Omit for a workspace-level label
  --parent <label-group>  Nest under an existing label group
  --is-group              Mark this label as a group container`,
  },
  status: {
    _overview: `Usage: ${CLI_NAME} status <list|get>`,
    list: `Usage: ${CLI_NAME} status list --team <team>

Required:
  --team <name-or-id>`,
    get: `Usage: ${CLI_NAME} status get --id <status-id> --name <status-name> --team <team>

All three flags are required by the MCP schema.`,
  },
  comment: {
    _overview: `Usage: ${CLI_NAME} comment <list|save|delete>`,
    list: `Usage: ${CLI_NAME} comment list --issue-id <issue-id> [flags]

Required:
  --issue-id <uuid-or-key>

Flags:
  --cursor --limit --order-by createdAt|updatedAt`,
    save: `Usage: ${CLI_NAME} comment save --body <markdown> [--id <comment-id> | --issue-id <issue-id>]

Create:
  --issue-id <issue-id> --body <markdown> [--parent-id <comment-id> for replies]
Update:
  --id <comment-id> --body <markdown>`,
    delete: `Usage: ${CLI_NAME} comment delete --id <comment-id>

Required:
  --id <comment-id>`,
  },
  attachment: {
    _overview: `Usage: ${CLI_NAME} attachment <get|create|delete>`,
    get: `Usage: ${CLI_NAME} attachment get --id <attachment-id>`,
    create: `Usage: ${CLI_NAME} attachment create --issue <issue-id> (--file <path> | --base64 <content> --filename <name> --content-type <mime>) [flags]

Preferred:
  --file <path>          Reads, base64-encodes, and infers filename and MIME.

Advanced:
  --base64 <content>     Raw payload; requires --filename and --content-type.

Optional:
  --title <title>
  --subtitle <subtitle>`,
    delete: `Usage: ${CLI_NAME} attachment delete --id <attachment-id>`,
  },
  cycle: {
    _overview: `Usage: ${CLI_NAME} cycle list --team-id <team-uuid> [--type current|previous|next]`,
    list: `Usage: ${CLI_NAME} cycle list --team-id <team-uuid> [--type current|previous|next]

Required:
  --team-id <team-uuid>      Must be a UUID (not a team key or name).

Optional:
  --type current|previous|next`,
  },
  document: {
    _overview: `Usage: ${CLI_NAME} document <list|get|create|update>`,
    list: `Usage: ${CLI_NAME} document list [filters]

Flags:
  --query --project-id --initiative-id --creator-id
  --created-at --updated-at --include-archived
  --cursor --limit --order-by createdAt|updatedAt`,
    get: `Usage: ${CLI_NAME} document get --id <id-or-slug>`,
    create: `Usage: ${CLI_NAME} document create --title <title> [fields]

Required:
  --title <title>

Fields:
  --content <markdown>
  --project <project>   Attach to a project
  --issue <issue-id>    Or attach to an issue (mutually exclusive with --project)
  --icon <emoji>
  --color <hex>`,
    update: `Usage: ${CLI_NAME} document update --id <id-or-slug> [fields]

Required:
  --id <id-or-slug>

Fields:
  --title --content --project --icon --color

Note: updating an issue-attached document to a project is allowed; the reverse is not.`,
  },
  milestone: {
    _overview: `Usage: ${CLI_NAME} milestone <list|get|save>`,
    list: `Usage: ${CLI_NAME} milestone list --project <project>`,
    get: `Usage: ${CLI_NAME} milestone get --project <project> --query <milestone-name-or-id>

Both flags required.`,
    save: `Usage: ${CLI_NAME} milestone save --project <project> [--id <id> | --name <name>] [fields]

Required:
  --project <project>
Create:
  --name <name>
Update:
  --id <milestone-name-or-id>

Fields:
  --description <text>
  --target-date <iso>   or --clear-target-date  (null-to-remove)

Note: milestone status (done, next, overdue, unstarted) is read-only.
Linear derives it from the target date and the completion state of the
milestone's issues. To change status, complete the issues or adjust
--target-date.`,
  },
  team: {
    _overview: `Usage: ${CLI_NAME} team <list|get>`,
    list: `Usage: ${CLI_NAME} team list [flags]

Flags:
  --query <search>
  --include-archived
  --created-at --updated-at
  --cursor --limit --order-by createdAt|updatedAt`,
    get: `Usage: ${CLI_NAME} team get --query <uuid-key-or-name>`,
  },
  user: {
    _overview: `Usage: ${CLI_NAME} user <list|get>`,
    list: `Usage: ${CLI_NAME} user list [flags]

Flags:
  --query <name-or-email>
  --team <team>
  --cursor --limit --order-by createdAt|updatedAt`,
    get: `Usage: ${CLI_NAME} user get --query <id-name-email-or-me>`,
  },
  image: {
    _overview: `Usage: ${CLI_NAME} image extract (--markdown <content> | --from-file <path>)`,
    extract: `Usage: ${CLI_NAME} image extract (--markdown <content> | --from-file <path>)

Resolves images embedded in Markdown content through the authenticated session.
May return binary content; the raw MCP payload is surfaced under 'result'.`,
  },
  docs: {
    _overview: `Usage: ${CLI_NAME} docs search --query <text> [--page <n>]`,
    search: `Usage: ${CLI_NAME} docs search --query <text> [--page <n>]

Searches Linear's help docs. Returns ranked results under 'results'.`,
  },
};

function printCommandHelp(command, subcommand) {
  const group = COMMAND_HELP[command];
  if (!group) {
    printHelp();
    return;
  }
  if (subcommand && group[subcommand]) {
    console.log(group[subcommand]);
    return;
  }
  console.log(group._overview ?? `No help available for ${command}`);
}

function printHelp() {
  console.log(`${CLI_NAME}

Usage:
  ${CLI_NAME} <command> [options]

Commands:
  auth login|logout|status
  mcp discover
  project list|get|save
  project-label list
  issue list|get|save
  issue-label list|create
  status list|get
  comment list|save|delete
  attachment get|create|delete
  cycle list
  document list|get|create|update
  milestone list|get|save
  team list|get
  user list|get
  image extract
  docs search

Global flags:
  --api-key <key>              Override persisted auth for this invocation

Project list flags:
  --team <team> --query <query> --state <state> --initiative <initiative>
  --member <member> --label <label> --created-at <iso> --updated-at <iso>
  --cursor --limit --order-by createdAt|updatedAt
  --include-milestones --include-members --include-archived

Project get flags:
  --query <query> --include-milestones --include-members --include-resources

Project save flags:
  --id <project-id> --name <name> --icon --color --summary --description
  --state --start-date --start-date-resolution halfYear|month|quarter|year
  --target-date --target-date-resolution halfYear|month|quarter|year
  --priority 0-4 --lead <user>
  --add-team --remove-team --set-team (repeatable)
  --labels (repeatable)
  --add-initiative --remove-initiative --set-initiative (repeatable)

Project-label list flags:
  --name <filter> --cursor --limit --order-by createdAt|updatedAt

Issue list flags:
  --query --team --state --cycle --label --assignee --delegate --project
  --priority 0-4 --parent-id --created-at --updated-at --include-archived
  --cursor --limit --order-by createdAt|updatedAt
  --unassigned                 Filter to issues with no assignee (mutually exclusive with --assignee)

Issue get flags:
  --id <issue-id> --include-relations --include-customer-needs

Issue save flags:
  --id <issue-id>           Required when updating
  --title <title>           Required when creating
  --team <team>             Required when creating
  --description <markdown>  Literal Markdown, no escape sequences
  --project --state --assignee --delegate --priority 0-4
  --labels (repeatable)
  --due-date --parent --estimate --cycle --milestone
  --link "url|title"        Repeatable. Append-only.
  --blocks --blocked-by --related-to  (repeatable, append-only)
  --duplicate-of <issue>
  --remove-blocks --remove-blocked-by --remove-related-to (repeatable)

Issue-label list flags:
  --name <filter> --team <team> --cursor --limit --order-by

Issue-label create flags:
  --name <name> [--description --color --team-id --parent --is-group]

Status list flags:
  --team <team>

Status get flags:
  --id <status-id> --name <status-name> --team <team>   (all three required)

Comment list flags:
  --issue-id <issue-id> --cursor --limit --order-by

Comment save flags:
  --id <comment-id>         When updating
  --issue-id <issue-id>     Required when creating
  --parent-id <comment-id>  Optional reply target when creating
  --body <markdown>         Required

Comment delete flags:
  --id <comment-id>

Attachment get flags:
  --id <attachment-id>

Attachment create flags:
  --issue <issue-id>                 Required
  --file <path>                      Preferred: reads and base64-encodes the file
  --base64 <content>                 Alternative raw payload
  --filename <name>                  Required with --base64, optional with --file
  --content-type <mime>              Required with --base64, optional with --file
  --title --subtitle                 Optional

Attachment delete flags:
  --id <attachment-id>

Cycle list flags:
  --team-id <team-uuid>             Required (team UUID only, not name)
  --type current|previous|next

Document list flags:
  --query --project-id --initiative-id --creator-id
  --created-at --updated-at --include-archived
  --cursor --limit --order-by

Document get flags:
  --id <document-id-or-slug>

Document create flags:
  --title <title>           Required
  --content <markdown>      Literal Markdown
  --project <project> | --issue <issue-id>
  --icon <emoji> --color <hex>

Document update flags:
  --id <document-id-or-slug>  Required
  --title --content --project --icon --color

Milestone list flags:
  --project <project>       Required

Milestone get flags:
  --project <project> --query <milestone-name-or-id>   (both required)

Milestone save flags:
  --project <project>       Required
  --id <milestone>          When updating (name or ID)
  --name <name>             Required when creating
  --description --target-date

Team list flags:
  --query --include-archived --created-at --updated-at --cursor --limit --order-by

Team get flags:
  --query <team-uuid-key-or-name>

User list flags:
  --query <name-or-email> --team --cursor --limit --order-by

User get flags:
  --query <user-id-name-email-or-me>

Image extract flags:
  --markdown <content>      Inline Markdown containing image references
  --from-file <path>        Read Markdown from a file instead

Docs search flags:
  --query <text>            Required
  --page <n>

Notes:
  - Output is JSON-first.
  - Every command maps 1:1 to a Linear MCP tool, exposed under the authenticated session.
  - Null-to-remove semantics (e.g., clearing assignee, parent, duplicateOf) are not exposed;
    use the Linear UI for explicit clears until the CLI grows a safer path.

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
    lead: resolveClearable(parsed, "--lead", "--clear-lead"),
    addTeams: getFlagList(parsed, "--add-team"),
    removeTeams: getFlagList(parsed, "--remove-team"),
    setTeams: getFlagList(parsed, "--set-team"),
    labels: getFlagList(parsed, "--labels"),
    addInitiatives: getFlagList(parsed, "--add-initiative"),
    removeInitiatives: getFlagList(parsed, "--remove-initiative"),
    setInitiatives: getFlagList(parsed, "--set-initiative"),
  };
}

function resolveClearable(parsed, valueFlag, clearFlag) {
  const value = getFlag(parsed, valueFlag);
  const clear = getBooleanFlag(parsed, clearFlag);
  if (clear && value) {
    throw new Error(`${valueFlag} and ${clearFlag} are mutually exclusive`);
  }
  if (clear) return null;
  return value;
}

function normalizeIssueSaveInput(parsed) {
  return {
    id: getFlag(parsed, "--id"),
    title: getFlag(parsed, "--title"),
    description: getFlag(parsed, "--description"),
    team: getFlag(parsed, "--team"),
    cycle: getFlag(parsed, "--cycle"),
    milestone: getFlag(parsed, "--milestone"),
    priority: asInteger(getFlag(parsed, "--priority"), { flag: "--priority", min: 0, max: 4 }),
    project: getFlag(parsed, "--project"),
    state: getFlag(parsed, "--state"),
    assignee: resolveClearable(parsed, "--assignee", "--clear-assignee"),
    delegate: resolveClearable(parsed, "--delegate", "--clear-delegate"),
    labels: getFlagList(parsed, "--labels"),
    dueDate: getFlag(parsed, "--due-date"),
    parentId: resolveClearable(parsed, "--parent", "--clear-parent"),
    estimate: getFlag(parsed, "--estimate"),
    links: getFlagList(parsed, "--link"),
    blocks: getFlagList(parsed, "--blocks"),
    blockedBy: getFlagList(parsed, "--blocked-by"),
    relatedTo: getFlagList(parsed, "--related-to"),
    duplicateOf: resolveClearable(parsed, "--duplicate-of", "--clear-duplicate-of"),
    removeBlocks: getFlagList(parsed, "--remove-blocks"),
    removeBlockedBy: getFlagList(parsed, "--remove-blocked-by"),
    removeRelatedTo: getFlagList(parsed, "--remove-related-to"),
  };
}

function requiresProjectCreateTeamFlags(parsed) {
  return getFlag(parsed, "--name") && !getFlag(parsed, "--id") && !getFlagList(parsed, "--add-team") && !getFlagList(parsed, "--set-team");
}

export async function main(argv = process.argv.slice(2)) {
  const parsed = parseArgs(argv);
  const [command, subcommand] = parsed.positionals;

  const helpRequested = parsed.values.has("--help") || parsed.values.has("-h") || command === "help";
  if (!command || (helpRequested && !COMMAND_HELP[command])) {
    printHelp();
    return 0;
  }
  if (helpRequested) {
    printCommandHelp(command, subcommand);
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
              ...commonListOptions(parsed),
            });
            output("project list", result);
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
            output("project get", { project });
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
            output("project save", { project });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli project <list|get|save>");
        }
      }

      case "project-label": {
        if (subcommand !== "list") throw new Error("Usage: linear-cli project-label list");
        const result = await listProjectLabels({
          name: getFlag(parsed, "--name"),
          ...commonListOptions(parsed),
        });
        output("project-label list", result);
        return 0;
      }

      case "issue": {
        switch (subcommand) {
          case "list": {
            const defaults = await resolveConfigDefaults({ defaultTeam: getFlag(parsed, "--team") });
            const result = await listIssues({
              team: defaults.defaultTeam,
              query: getFlag(parsed, "--query"),
              state: getFlag(parsed, "--state"),
              cycle: getFlag(parsed, "--cycle"),
              label: getFlag(parsed, "--label"),
              assignee: resolveClearable(parsed, "--assignee", "--unassigned"),
              delegate: getFlag(parsed, "--delegate"),
              project: getFlag(parsed, "--project"),
              priority: asInteger(getFlag(parsed, "--priority"), { flag: "--priority", min: 0, max: 4 }),
              parentId: getFlag(parsed, "--parent-id"),
              createdAt: getFlag(parsed, "--created-at"),
              updatedAt: getFlag(parsed, "--updated-at"),
              includeArchived: getBooleanFlag(parsed, "--include-archived"),
              ...commonListOptions(parsed),
            });
            output("issue list", result);
            return 0;
          }
          case "get": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli issue get --id <issue-id>");
            const issue = await getIssue(id, {
              includeRelations: getBooleanFlag(parsed, "--include-relations"),
              includeCustomerNeeds: getBooleanFlag(parsed, "--include-customer-needs"),
            });
            output("issue get", { issue });
            return 0;
          }
          case "save": {
            const input = normalizeIssueSaveInput(parsed);
            if (!input.id && !(input.title && input.team)) {
              throw new Error("Creating an issue requires --title and --team; updating requires --id");
            }
            const issue = await saveIssue(input);
            output("issue save", { issue });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli issue <list|get|save>");
        }
      }

      case "issue-label": {
        switch (subcommand) {
          case "list": {
            const result = await listIssueLabels({
              name: getFlag(parsed, "--name"),
              team: getFlag(parsed, "--team"),
              ...commonListOptions(parsed),
            });
            output("issue-label list", result);
            return 0;
          }
          case "create": {
            const name = getFlag(parsed, "--name");
            if (!name) throw new Error("Usage: linear-cli issue-label create --name <name>");
            const label = await createIssueLabel({
              name,
              description: getFlag(parsed, "--description"),
              color: getFlag(parsed, "--color"),
              teamId: getFlag(parsed, "--team-id"),
              parent: getFlag(parsed, "--parent"),
              isGroup: getBooleanFlag(parsed, "--is-group"),
            });
            output("issue-label create", { label });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli issue-label <list|create>");
        }
      }

      case "status": {
        switch (subcommand) {
          case "list": {
            const team = getFlag(parsed, "--team");
            if (!team) throw new Error("Usage: linear-cli status list --team <team>");
            const statuses = await listIssueStatuses(team);
            output("status list", Array.isArray(statuses) ? { statuses } : statuses);
            return 0;
          }
          case "get": {
            const id = getFlag(parsed, "--id");
            const name = getFlag(parsed, "--name");
            const team = getFlag(parsed, "--team");
            if (!id || !name || !team) {
              throw new Error("Usage: linear-cli status get --id <status-id> --name <status-name> --team <team>");
            }
            const status = await getIssueStatus({ id, name, team });
            output("status get", { status });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli status <list|get>");
        }
      }

      case "comment": {
        switch (subcommand) {
          case "list": {
            const issueId = getFlag(parsed, "--issue-id");
            if (!issueId) throw new Error("Usage: linear-cli comment list --issue-id <issue-id>");
            const comments = await listComments(issueId, commonListOptions(parsed));
            output("comment list", comments);
            return 0;
          }
          case "save": {
            const id = getFlag(parsed, "--id");
            const issueId = getFlag(parsed, "--issue-id");
            const body = getFlag(parsed, "--body");
            if (!body) throw new Error("Usage: linear-cli comment save --body <markdown> [--id <comment-id> | --issue-id <issue-id>]");
            if (!id && !issueId) {
              throw new Error("Creating a comment requires --issue-id; updating requires --id");
            }
            const comment = await saveComment({
              id,
              issueId,
              parentId: getFlag(parsed, "--parent-id"),
              body,
            });
            output("comment save", { comment });
            return 0;
          }
          case "delete": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli comment delete --id <comment-id>");
            const result = await deleteComment(id);
            output("comment delete", { result });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli comment <list|save|delete>");
        }
      }

      case "attachment": {
        switch (subcommand) {
          case "get": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli attachment get --id <attachment-id>");
            const attachment = await getAttachment(id);
            output("attachment get", { attachment });
            return 0;
          }
          case "create": {
            const issue = getFlag(parsed, "--issue");
            if (!issue) throw new Error("Usage: linear-cli attachment create --issue <issue-id> --file <path> [--title ...]");
            const payload = await resolveAttachmentPayload(parsed);
            const attachment = await createAttachment({
              issue,
              ...payload,
              title: getFlag(parsed, "--title"),
              subtitle: getFlag(parsed, "--subtitle"),
            });
            output("attachment create", { attachment });
            return 0;
          }
          case "delete": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli attachment delete --id <attachment-id>");
            const result = await deleteAttachment(id);
            output("attachment delete", { result });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli attachment <get|create|delete>");
        }
      }

      case "cycle": {
        if (subcommand !== "list") throw new Error("Usage: linear-cli cycle list --team-id <team-uuid>");
        const teamId = getFlag(parsed, "--team-id");
        if (!teamId) throw new Error("Usage: linear-cli cycle list --team-id <team-uuid>");
        const type = assertChoice("--type", getFlag(parsed, "--type"), ["current", "previous", "next"]);
        const cycles = await listCycles(teamId, { type });
        output("cycle list", Array.isArray(cycles) ? { cycles } : cycles);
        return 0;
      }

      case "document": {
        switch (subcommand) {
          case "list": {
            const result = await listDocuments({
              query: getFlag(parsed, "--query"),
              projectId: getFlag(parsed, "--project-id"),
              initiativeId: getFlag(parsed, "--initiative-id"),
              creatorId: getFlag(parsed, "--creator-id"),
              createdAt: getFlag(parsed, "--created-at"),
              updatedAt: getFlag(parsed, "--updated-at"),
              includeArchived: getBooleanFlag(parsed, "--include-archived"),
              ...commonListOptions(parsed),
            });
            output("document list", result);
            return 0;
          }
          case "get": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli document get --id <document-id-or-slug>");
            const document = await getDocument(id);
            output("document get", { document });
            return 0;
          }
          case "create": {
            const title = getFlag(parsed, "--title");
            if (!title) throw new Error("Usage: linear-cli document create --title <title> [fields]");
            const document = await createDocument({
              title,
              content: getFlag(parsed, "--content"),
              project: getFlag(parsed, "--project"),
              issue: getFlag(parsed, "--issue"),
              icon: getFlag(parsed, "--icon"),
              color: getFlag(parsed, "--color"),
            });
            output("document create", { document });
            return 0;
          }
          case "update": {
            const id = getFlag(parsed, "--id");
            if (!id) throw new Error("Usage: linear-cli document update --id <document-id-or-slug> [fields]");
            const document = await updateDocument({
              id,
              title: getFlag(parsed, "--title"),
              content: getFlag(parsed, "--content"),
              project: getFlag(parsed, "--project"),
              icon: getFlag(parsed, "--icon"),
              color: getFlag(parsed, "--color"),
            });
            output("document update", { document });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli document <list|get|create|update>");
        }
      }

      case "milestone": {
        switch (subcommand) {
          case "list": {
            const project = getFlag(parsed, "--project");
            if (!project) throw new Error("Usage: linear-cli milestone list --project <project>");
            const milestones = await listMilestones(project);
            output("milestone list", Array.isArray(milestones) ? { milestones } : milestones);
            return 0;
          }
          case "get": {
            const project = getFlag(parsed, "--project");
            const query = getFlag(parsed, "--query");
            if (!project || !query) {
              throw new Error("Usage: linear-cli milestone get --project <project> --query <milestone-name-or-id>");
            }
            const milestone = await getMilestone(project, query);
            output("milestone get", { milestone });
            return 0;
          }
          case "save": {
            const project = getFlag(parsed, "--project");
            const id = getFlag(parsed, "--id");
            const name = getFlag(parsed, "--name");
            if (!project) throw new Error("milestone save requires --project");
            if (!id && !name) throw new Error("milestone save requires --id (update) or --name (create)");
            const milestone = await saveMilestone({
              project,
              id,
              name,
              description: getFlag(parsed, "--description"),
              targetDate: resolveClearable(parsed, "--target-date", "--clear-target-date"),
            });
            output("milestone save", { milestone });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli milestone <list|get|save>");
        }
      }

      case "team": {
        switch (subcommand) {
          case "list": {
            const result = await listTeams({
              query: getFlag(parsed, "--query"),
              createdAt: getFlag(parsed, "--created-at"),
              updatedAt: getFlag(parsed, "--updated-at"),
              includeArchived: getBooleanFlag(parsed, "--include-archived"),
              ...commonListOptions(parsed),
            });
            output("team list", result);
            return 0;
          }
          case "get": {
            const query = getFlag(parsed, "--query");
            if (!query) throw new Error("Usage: linear-cli team get --query <team-uuid-key-or-name>");
            const team = await getTeam(query);
            output("team get", { team });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli team <list|get>");
        }
      }

      case "user": {
        switch (subcommand) {
          case "list": {
            const result = await listUsers({
              query: getFlag(parsed, "--query"),
              team: getFlag(parsed, "--team"),
              ...commonListOptions(parsed),
            });
            output("user list", result);
            return 0;
          }
          case "get": {
            const query = getFlag(parsed, "--query");
            if (!query) throw new Error("Usage: linear-cli user get --query <id-name-email-or-me>");
            const user = await getUser(query);
            output("user get", { user });
            return 0;
          }
          default:
            throw new Error("Usage: linear-cli user <list|get>");
        }
      }

      case "image": {
        if (subcommand !== "extract") throw new Error("Usage: linear-cli image extract --markdown <content> | --from-file <path>");
        const inline = getFlag(parsed, "--markdown");
        const fromFile = getFlag(parsed, "--from-file");
        if (!inline && !fromFile) {
          throw new Error("image extract requires --markdown <content> or --from-file <path>");
        }
        const markdown = inline ?? (await readFile(fromFile, "utf8"));
        const result = await extractImages(markdown);
        output("image extract", { result });
        return 0;
      }

      case "docs": {
        if (subcommand !== "search") throw new Error("Usage: linear-cli docs search --query <text>");
        const query = getFlag(parsed, "--query");
        if (!query) throw new Error("Usage: linear-cli docs search --query <text>");
        const result = await searchDocumentation(query, {
          page: asInteger(getFlag(parsed, "--page"), { flag: "--page", min: 1 }),
        });
        const results = result && typeof result === "object" && !Array.isArray(result) ? Object.values(result) : result;
        output("docs search", { results });
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
