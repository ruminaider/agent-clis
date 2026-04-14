#!/usr/bin/env node

import { readFile } from "node:fs/promises";
import { login, loadCredentials, clearCredentials, refreshToken } from "../lib/auth.js";
import * as api from "../lib/api.js";
import { listTools } from "../lib/mcp.js";

const args = process.argv.slice(2);
const BOOLEAN_FLAGS = new Set(["--all", "--allow-deleting-content", "--help", "-h"]);
const VALUE_FLAGS = new Set([
  "-f",
  "--format",
  "--port",
  "--parent",
  "--title",
  "--content",
  "--find",
  "--find-file",
  "--replace",
  "--replace-file",
  "--edits-file",
  "--schema",
  "--ddl",
]);
const ALL_FLAGS = new Set([...BOOLEAN_FLAGS, ...VALUE_FLAGS]);

let parsedArgs;
try {
  parsedArgs = (() => {
    const positionals = [];
    const values = new Map();

    for (let i = 0; i < args.length; i++) {
      const arg = args[i];

      if (!arg.startsWith("-") || arg === "-") {
        positionals.push(arg);
        continue;
      }

      const eqIndex = arg.indexOf("=");
      if (eqIndex !== -1) {
        const flag = arg.slice(0, eqIndex);
        const value = arg.slice(eqIndex + 1);
        if (BOOLEAN_FLAGS.has(flag)) {
          throw new Error(`Flag ${flag} does not take a value`);
        }
        if (!VALUE_FLAGS.has(flag)) {
          throw new Error(`Unknown flag: ${flag}`);
        }
        values.set(flag, value);
        continue;
      }

      if (BOOLEAN_FLAGS.has(arg)) {
        values.set(arg, true);
        continue;
      }

      if (VALUE_FLAGS.has(arg)) {
        const value = args[i + 1];
        if (value === undefined || ALL_FLAGS.has(value)) {
          throw new Error(`Flag ${arg} requires a value`);
        }
        values.set(arg, value);
        i++;
        continue;
      }

      throw new Error(`Unknown flag: ${arg}`);
    }

    return { positionals, values };
  })();
} catch (err) {
  console.error(`Error: ${err.message}`);
  process.exit(1);
}

const command = parsedArgs.positionals[0];
const subcommand = parsedArgs.positionals[1];

function getFlag(name) {
  return parsedArgs.values.get(name);
}

function hasFlag(name) {
  return parsedArgs.values.has(name);
}

function getFormat() {
  return getFlag("-f") ?? getFlag("--format");
}

function getArg(index) {
  return parsedArgs.positionals[index];
}

function output(data, format) {
  if (format === "json" || !process.stdout.isTTY) {
    console.log(JSON.stringify(data, null, 2));
  } else if (typeof data === "string") {
    console.log(data);
  } else {
    console.log(JSON.stringify(data, null, 2));
  }
}

async function readTextInput(flagName, fileFlagName) {
  const inline = getFlag(flagName);
  const filePath = getFlag(fileFlagName);

  if (inline !== undefined && filePath !== undefined) {
    throw new Error(`Use either ${flagName} or ${fileFlagName}, not both`);
  }

  if (filePath !== undefined) {
    return readFile(filePath, "utf8");
  }

  return inline;
}

function normalizeContentUpdate(update, index) {
  if (!update || typeof update !== "object" || Array.isArray(update)) {
    throw new Error(`Invalid content update at index ${index}`);
  }
  if (typeof update.old_str !== "string" || update.old_str.length === 0) {
    throw new Error(`content_updates[${index}].old_str must be a non-empty string`);
  }
  if (typeof update.new_str !== "string") {
    throw new Error(`content_updates[${index}].new_str must be a string`);
  }
  if (Object.prototype.hasOwnProperty.call(update, "replace_all_matches") && typeof update.replace_all_matches !== "boolean") {
    throw new Error(`content_updates[${index}].replace_all_matches must be a boolean`);
  }

  const normalized = {
    old_str: update.old_str,
    new_str: update.new_str,
  };

  if (typeof update.replace_all_matches === "boolean") {
    normalized.replace_all_matches = update.replace_all_matches;
  }

  return normalized;
}

async function loadEditsFile(path) {
  let raw;
  try {
    raw = await readFile(path, "utf8");
  } catch (err) {
    throw new Error(`Failed to read edits file: ${err.message}`);
  }

  let parsed;
  try {
    parsed = JSON.parse(raw);
  } catch (err) {
    throw new Error(`Failed to parse edits file: ${err.message}`);
  }

  let contentUpdates;

  if (Array.isArray(parsed)) {
    contentUpdates = parsed;
  } else if (parsed && typeof parsed === "object" && Array.isArray(parsed.content_updates)) {
    if (Object.prototype.hasOwnProperty.call(parsed, "allow_deleting_content")) {
      throw new Error("Use --allow-deleting-content on the CLI, not in the edits file");
    }
    contentUpdates = parsed.content_updates;
  } else {
    throw new Error("Edits file must be a JSON array or an object with content_updates");
  }

  if (contentUpdates.length === 0) {
    throw new Error("Edits file must include at least one content update");
  }

  return contentUpdates.map(normalizeContentUpdate);
}

function printPageHelp() {
  console.log(`Usage: notion-cli page <create|edit|update|move|duplicate>

  create --parent <id> --title <title> [--content <markdown>]
  edit <page-id> --find <text> --replace <text> [--all] [--allow-deleting-content]
  edit <page-id> --find-file <path> --replace-file <path> [--all] [--allow-deleting-content]
  edit <page-id> --edits-file <path> [--allow-deleting-content]
  update <page-id> [--title <title>] [--content <markdown>] [--allow-deleting-content]
  move <page-id> --parent <new-parent-id>
  duplicate <page-id>

Notes:
  - page edit performs exact-match search and replace using the current page content.
  - page edit --edits-file accepts a JSON array, or {"content_updates":[...]} for batch edits.
  - In batch mode, set replace_all_matches on each update. Use --all only with inline or file inputs.
  - page update --content replaces the entire page body. Use --allow-deleting-content when the replacement deletes content, including empty strings.`);
}

async function main() {
  try {
    switch (command) {
      case "auth":      await cmdAuth(); break;
      case "search":    await cmdSearch(); break;
      case "fetch":     await cmdFetch(); break;
      case "page":      await cmdPage(); break;
      case "db":        await cmdDb(); break;
      case "comment":   await cmdComment(); break;
      case "users":     await cmdUsers(); break;
      case "teams":     await cmdTeams(); break;
      case "tools":     await cmdTools(); break;
      case "help": case "--help": case "-h": case undefined:
        printHelp(); break;
      default:
        console.error(`Unknown command: ${command}`);
        printHelp();
        process.exit(1);
    }
  } catch (err) {
    if (err.message?.includes("Not authenticated")) {
      console.error("Not authenticated. Run: notion-cli auth login");
    } else {
      console.error(`Error: ${err.message}`);
    }
    process.exit(1);
  }
}

async function cmdAuth() {
  switch (subcommand) {
    case undefined:
    case "login": {
      const existing = await loadCredentials();
      if (existing?.refresh_token) {
        try {
          const refreshed = await refreshToken(existing);
          const mins = Math.round((refreshed.expires_at - Date.now()) / 60000);
          console.log(`✓ Authenticated (token refreshed, expires in ${mins} minutes)`);
          break;
        } catch {
          console.log("Token refresh failed, opening browser for re-authorization...\n");
        }
      }
      const port = parseInt(getFlag("--port") || "9876");
      const result = await login(port);
      console.log(`\n✓ Authenticated with Notion`);
      console.log(`  Token obtained: ${result.obtained_at}`);
      break;
    }
    case "logout": {
      await clearCredentials();
      console.log("✓ Logged out");
      break;
    }
    case "status": {
      const creds = await loadCredentials();
      if (!creds?.access_token) {
        console.log("✗ Not authenticated");
        console.log("  Run: notion-cli auth login");
        process.exit(1);
      }
      console.log("✓ Authenticated");
      if (creds.obtained_at) console.log(`  Authenticated: ${creds.obtained_at}`);
      if (creds.expires_at) {
        const remaining = creds.expires_at - Date.now();
        if (remaining > 0) {
          console.log(`  Token expires in: ${Math.round(remaining / 60000)} minutes`);
        } else {
          console.log("  Token expired (will auto-refresh)");
        }
      }
      if (creds.refreshed_at) console.log(`  Last refreshed: ${creds.refreshed_at}`);
      break;
    }
    default:
      console.log(`Usage: notion-cli auth [login|logout|status]\n\n  login [--port <port>]   Authenticate (refreshes token, or opens browser if needed)\n  logout                  Clear credentials\n  status                  Show auth status`);
  }
}

async function cmdSearch() {
  const query = getArg(1);
  if (!query) {
    console.error("Usage: notion-cli search <query>");
    process.exit(1);
  }
  output(await api.search(query), getFormat());
}

async function cmdFetch() {
  const url = getArg(1);
  if (!url) {
    console.error("Usage: notion-cli fetch <page-url-or-id>");
    process.exit(1);
  }
  output(await api.getPage(url), getFormat());
}

async function cmdPage() {
  const format = getFormat();

  if (hasFlag("--help") || hasFlag("-h") || subcommand === undefined) {
    printPageHelp();
    return;
  }

  switch (subcommand) {
    case "create": {
      const parentId = getFlag("--parent");
      const title = getFlag("--title");
      if (!parentId || !title) {
        console.error("Usage: notion-cli page create --parent <id> --title <title> [--content <markdown>]");
        process.exit(1);
      }
      const content = getFlag("--content");
      output(await api.createPage(parentId, title, content), format);
      break;
    }
    case "edit": {
      const pageId = getArg(2);
      if (!pageId) {
        console.error("Usage: notion-cli page edit <page-id> --find <text> --replace <text> [--all]");
        process.exit(1);
      }

      const editsFile = getFlag("--edits-file");
      const replaceAllMatches = hasFlag("--all");
      let allowDeletingContent = hasFlag("--allow-deleting-content");
      let contentUpdates;

      if (editsFile) {
        if (replaceAllMatches) {
          throw new Error("--all cannot be used with --edits-file; set replace_all_matches in the edits file instead");
        }
        if (hasFlag("--find") || hasFlag("--find-file") || hasFlag("--replace") || hasFlag("--replace-file")) {
          throw new Error("Use either --edits-file or --find/--replace inputs, not both");
        }
        contentUpdates = await loadEditsFile(editsFile);
      } else {
        const find = await readTextInput("--find", "--find-file");
        const replace = await readTextInput("--replace", "--replace-file");

        if (find === undefined || replace === undefined) {
          throw new Error("page edit requires --find and --replace, or --edits-file");
        }
        if (find.length === 0) {
          throw new Error("--find must be a non-empty string");
        }
        contentUpdates = [{
          old_str: find,
          new_str: replace,
          ...(replaceAllMatches ? { replace_all_matches: true } : {}),
        }];
      }

      output(await api.editPageContent(pageId, contentUpdates, { allowDeletingContent }), format);
      break;
    }
    case "update": {
      const pageId = getArg(2);
      if (!pageId) {
        console.error("Usage: notion-cli page update <page-id> [--title <title>] [--content <md>] [--allow-deleting-content]");
        process.exit(1);
      }
      const updates = {};
      const title = getFlag("--title");
      const content = getFlag("--content");
      if (title) updates.title = title;
      if (content !== undefined) updates.content = content;
      if (hasFlag("--allow-deleting-content")) updates.allowDeletingContent = true;
      if (updates.title === undefined && updates.content === undefined) {
        throw new Error("page update requires --title or --content");
      }
      output(await api.updatePage(pageId, updates), format);
      break;
    }
    case "move": {
      const pageId = getArg(2);
      const newParent = getFlag("--parent");
      if (!pageId || !newParent) {
        console.error("Usage: notion-cli page move <page-id> --parent <new-parent-id>");
        process.exit(1);
      }
      output(await api.movePages([pageId], newParent), format);
      break;
    }
    case "duplicate": {
      const pageId = getArg(2);
      if (!pageId) {
        console.error("Usage: notion-cli page duplicate <page-id>");
        process.exit(1);
      }
      output(await api.duplicatePage(pageId), format);
      break;
    }
    default:
      printPageHelp();
  }
}

async function cmdDb() {
  const format = getFormat();

  switch (subcommand) {
    case "create": {
      const parentId = getFlag("--parent");
      const schema = getFlag("--schema") || getFlag("--ddl");
      const title = getFlag("--title");
      if (!parentId || !schema) {
        console.error('Usage: notion-cli db create --parent <id> --schema "CREATE TABLE ..." [--title <title>]');
        process.exit(1);
      }
      output(await api.createDatabase(parentId, schema, title), format);
      break;
    }
    case "update": {
      const dsId = getArg(2);
      const statements = getFlag("--schema") || getFlag("--ddl");
      if (!dsId || !statements) {
        console.error('Usage: notion-cli db update <data-source-id> --schema "ALTER TABLE ..."');
        process.exit(1);
      }
      output(await api.updateDataSource(dsId, statements), format);
      break;
    }
    default:
      console.log(`Usage: notion-cli db <create|update>\n\n  create --parent <id> --schema "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))" [--title <title>]\n  update <data-source-id> --schema "ALTER TABLE ..."`);
  }
}

async function cmdComment() {
  const format = getFormat();

  switch (subcommand) {
    case "list": {
      const pageId = getArg(2);
      if (!pageId) {
        console.error("Usage: notion-cli comment list <page-id>");
        process.exit(1);
      }
      output(await api.getComments(pageId), format);
      break;
    }
    case "add": {
      const pageId = getArg(2);
      const text = getArg(3);
      if (!pageId || !text) {
        console.error("Usage: notion-cli comment add <page-id> <text>");
        process.exit(1);
      }
      output(await api.addComment(pageId, text), format);
      break;
    }
    default:
      console.log(`Usage: notion-cli comment <list|add>\n\n  list <page-id>          List comments and discussions\n  add <page-id> <text>    Add a comment`);
  }
}

async function cmdUsers() {
  output(await api.listUsers(), getFormat());
}

async function cmdTeams() {
  output(await api.listTeams(), getFormat());
}

async function cmdTools() {
  const tools = await listTools();
  for (const tool of tools) {
    console.log(`${tool.name}`);
    console.log(`  ${(tool.description || "").split("\n")[0]}`);
    console.log();
  }
}

function printHelp() {
  console.log(`notion-cli: Notion CLI with full workspace access

Usage: notion-cli <command> [options]

Auth:
  auth [login]                 Authenticate (refreshes token, or opens browser)
  auth logout                  Clear credentials
  auth status                  Show auth status

Commands:
  search <query>               Search workspace
  fetch <page-url-or-id>       Fetch page or database content
  page create|edit|update|move|duplicate   Work with pages
  db create|update             Work with databases
  comment list|add             Work with comments
  users                        List workspace users
  teams                        List workspace teams
  tools                        List available MCP tools (debug)

Flags:
  -f, --format <json>          Force JSON output

Get started:
  notion-cli auth`);
}

main();
