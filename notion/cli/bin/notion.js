#!/usr/bin/env node

import { login, loadCredentials, clearCredentials, refreshToken } from "../lib/auth.js";
import * as api from "../lib/api.js";
import { listTools } from "../lib/mcp.js";

const args = process.argv.slice(2);
const command = args[0];
const subcommand = args[1];

// Parse flags
function getFlag(name) {
  const i = args.indexOf(name);
  if (i === -1) return undefined;
  return args[i + 1];
}
function hasFlag(name) {
  return args.includes(name);
}
function getFormat() {
  return getFlag("-f") || getFlag("--format");
}

// Get positional arg after command/subcommand
function getArg(index) {
  let pos = 0;
  for (let i = 0; i < args.length; i++) {
    if (args[i].startsWith("-")) {
      i++; // skip flag value
      continue;
    }
    if (pos === index) return args[i];
    pos++;
  }
  return undefined;
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

// ─── Auth Commands ───────────────────────────────────────

async function cmdAuth() {
  switch (subcommand) {
    case undefined:
    case "login": {
      // Try refresh first if we have credentials
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
          console.log(`  Token expired (will auto-refresh)`);
        }
      }
      if (creds.refreshed_at) console.log(`  Last refreshed: ${creds.refreshed_at}`);
      break;
    }
    default:
      console.log(`Usage: notion-cli auth [login|logout|status]\n\n  login [--port <port>]   Authenticate (refreshes token, or opens browser if needed)\n  logout                  Clear credentials\n  status                  Show auth status`);
  }
}

// ─── Search ──────────────────────────────────────────────

async function cmdSearch() {
  const query = getArg(1);
  if (!query) { console.error("Usage: notion-cli search <query>"); process.exit(1); }
  const format = getFormat();

  const result = await api.search(query);
  output(result, format);
}

// ─── Fetch ───────────────────────────────────────────────

async function cmdFetch() {
  const url = getArg(1);
  if (!url) { console.error("Usage: notion-cli fetch <page-url-or-id>"); process.exit(1); }
  const format = getFormat();

  const result = await api.getPage(url);
  output(result, format);
}

// ─── Page Commands ───────────────────────────────────────

async function cmdPage() {
  const format = getFormat();

  switch (subcommand) {
    case "create": {
      const parentId = getFlag("--parent");
      const title = getFlag("--title");
      if (!parentId || !title) {
        console.error("Usage: notion-cli page create --parent <id> --title <title> [--content <markdown>]");
        process.exit(1);
      }
      const content = getFlag("--content");
      const result = await api.createPage(parentId, title, content);
      output(result, format);
      break;
    }
    case "update": {
      const pageId = getArg(2);
      if (!pageId) { console.error("Usage: notion-cli page update <page-id> [--title <title>] [--content <md>]"); process.exit(1); }
      const updates = {};
      const title = getFlag("--title");
      const content = getFlag("--content");
      if (title) updates.title = title;
      if (content) updates.content = content;
      const result = await api.updatePage(pageId, updates);
      output(result, format);
      break;
    }
    case "move": {
      const pageId = getArg(2);
      const newParent = getFlag("--parent");
      if (!pageId || !newParent) {
        console.error("Usage: notion-cli page move <page-id> --parent <new-parent-id>");
        process.exit(1);
      }
      const result = await api.movePages([pageId], newParent);
      output(result, format);
      break;
    }
    case "duplicate": {
      const pageId = getArg(2);
      if (!pageId) { console.error("Usage: notion-cli page duplicate <page-id>"); process.exit(1); }
      const result = await api.duplicatePage(pageId);
      output(result, format);
      break;
    }
    default:
      console.log(`Usage: notion-cli page <create|update|move|duplicate>

  create --parent <id> --title <title> [--content <markdown>]
  update <page-id> [--title <title>] [--content <markdown>]
  move <page-id> --parent <new-parent-id>
  duplicate <page-id>`);
  }
}

// ─── Database Commands ───────────────────────────────────

async function cmdDb() {
  const format = getFormat();

  switch (subcommand) {
    case "create": {
      const parentId = getFlag("--parent");
      const ddl = getFlag("--ddl");
      if (!parentId || !ddl) {
        console.error('Usage: notion-cli db create --parent <id> --ddl "CREATE TABLE ..."');
        process.exit(1);
      }
      const result = await api.createDatabase(parentId, ddl);
      output(result, format);
      break;
    }
    case "update": {
      const dsId = getArg(2);
      const ddl = getFlag("--ddl");
      if (!dsId || !ddl) {
        console.error('Usage: notion-cli db update <data-source-id> --ddl "ALTER TABLE ..."');
        process.exit(1);
      }
      const result = await api.updateDataSource(dsId, ddl);
      output(result, format);
      break;
    }
    default:
      console.log(`Usage: notion-cli db <create|update>

  create --parent <id> --ddl "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
  update <data-source-id> --ddl "ALTER TABLE ..."`);
  }
}

// ─── Comment Commands ────────────────────────────────────

async function cmdComment() {
  const format = getFormat();

  switch (subcommand) {
    case "list": {
      const pageId = getArg(2);
      if (!pageId) { console.error("Usage: notion-cli comment list <page-id>"); process.exit(1); }
      const result = await api.getComments(pageId);
      output(result, format);
      break;
    }
    case "add": {
      const pageId = getArg(2);
      const text = getArg(3);
      if (!pageId || !text) { console.error("Usage: notion-cli comment add <page-id> <text>"); process.exit(1); }
      const result = await api.addComment(pageId, text);
      output(result, format);
      break;
    }
    default:
      console.log(`Usage: notion-cli comment <list|add>

  list <page-id>          List comments and discussions
  add <page-id> <text>    Add a comment`);
  }
}

// ─── Users & Teams ───────────────────────────────────────

async function cmdUsers() {
  const result = await api.listUsers();
  output(result, getFormat());
}

async function cmdTeams() {
  const result = await api.listTeams();
  output(result, getFormat());
}

// ─── Tools (debug) ───────────────────────────────────────

async function cmdTools() {
  const tools = await listTools();
  for (const tool of tools) {
    console.log(`${tool.name}`);
    console.log(`  ${(tool.description || "").split("\n")[0]}`);
    console.log();
  }
}

// ─── Help ────────────────────────────────────────────────

function printHelp() {
  console.log(`notion-cli — Notion CLI with full workspace access

Usage: notion-cli <command> [options]

Auth:
  auth [login]                 Authenticate (refreshes token, or opens browser)
  auth logout                  Clear credentials
  auth status                  Show auth status

Commands:
  search <query>               Search workspace
  fetch <page-url-or-id>       Fetch page/database content
  page create|update|move|duplicate   Work with pages
  db create|update             Work with databases
  comment list|add             Work with comments
  users                        List workspace users
  teams                        List workspace teams
  tools                        List available MCP tools (debug)

Flags:
  -f, --format <json>          Force JSON output

Get started:
  notion-cli auth login        Opens browser → click Approve → done`);
}

main();
