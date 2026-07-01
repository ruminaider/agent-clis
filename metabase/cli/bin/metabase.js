#!/usr/bin/env node

import { writeFile } from "node:fs/promises";
import * as auth from "../lib/auth.js";
import * as api from "../lib/api.js";
import { CLI_NAME, PACKAGE_VERSION } from "../lib/config.js";

const BOOLEAN_FLAGS = new Set([
  "--read-only",
  "--archived",
  "--include-cards",
  "--include-sensitive-fields",
  "--ignore-cache",
  "--json",
  "--help",
  "-h",
  "--version",
  "-v",
]);

const VALUE_FLAGS = new Set([
  "--url", "--api-key", "--session-token", "--email", "--password",
  "--f", "--model-id", "--name", "--description", "--display",
  "--dataset-query", "--collection-id", "--visualization-settings",
  "--parameters", "--parent-id", "--color", "--database-id", "--query",
  "--template-tags", "--format", "--output", "--limit", "--offset",
  "--models", "--q", "--entity", "--revision-id", "--model", "--database",
  "--dashboard", "--schema", "--cards", "--mbql", "--set",
  "--display-name", "--semantic-type", "--visibility-type",
]);

const ALL_FLAGS = new Set([...BOOLEAN_FLAGS, ...VALUE_FLAGS]);

function parseArgs(argv) {
  const positionals = [];
  const values = new Map();
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === "--") { positionals.push(...argv.slice(i + 1)); break; }
    if (!arg.startsWith("-") || arg === "-") { positionals.push(arg); continue; }
    const eq = arg.indexOf("=");
    if (eq !== -1) {
      const flag = arg.slice(0, eq);
      if (!VALUE_FLAGS.has(flag)) throw new Error(`Unknown or non-value flag: ${flag}`);
      values.set(flag, arg.slice(eq + 1));
      continue;
    }
    if (BOOLEAN_FLAGS.has(arg)) { values.set(arg, true); continue; }
    if (VALUE_FLAGS.has(arg)) {
      const next = argv[i + 1];
      if (next === undefined || ALL_FLAGS.has(next)) throw new Error(`Flag ${arg} requires a value`);
      values.set(arg, next); i++; continue;
    }
    throw new Error(`Unknown flag: ${arg}`);
  }
  return { positionals, values };
}

function out(data) {
  // A 204 No Content from a successful write returns undefined; report success.
  const payload = data === undefined ? { ok: true } : data;
  process.stdout.write(`${JSON.stringify(payload, null, 2)}\n`);
}
function fail(message, code = 1) {
  process.stderr.write(`${message}\n`);
  process.exit(code);
}
function need(value, label) {
  if (value === undefined || value === null || value === "") throw new Error(`Missing required ${label}`);
  return value;
}
function int(value, label) {
  const n = Number(value);
  if (!Number.isFinite(n)) throw new Error(`${label} must be a number, got: ${value}`);
  return n;
}
function ids(positionals, label) {
  if (positionals.length === 0) throw new Error(`Missing required ${label}`);
  return positionals.map((p) => int(p, label));
}
function parseJson(raw, flag) {
  try { return JSON.parse(raw); } catch { throw new Error(`${flag} must be valid JSON`); }
}
function json(values, flag) {
  const v = values.get(flag);
  return v === undefined ? undefined : parseJson(v, flag);
}
function requireJson(values, flag) {
  return parseJson(need(values.get(flag), flag), flag);
}
function list(values, flag) {
  const v = values.get(flag);
  return v === undefined ? undefined : String(v).split(",").map((s) => s.trim()).filter(Boolean);
}

function authOptions(values) {
  return {
    url: values.get("--url"),
    apiKey: values.get("--api-key"),
    sessionToken: values.get("--session-token"),
    email: values.get("--email"),
    password: values.get("--password"),
    readOnly: values.get("--read-only") === true,
  };
}

// Merge specific update flags plus a generic --set JSON object.
function updates(values, mapping) {
  const obj = { ...(json(values, "--set") || {}) };
  for (const [flag, key, kind] of mapping) {
    if (!values.has(flag)) continue;
    const raw = values.get(flag);
    obj[key] = kind === "int" ? int(raw, flag) : kind === "json" ? parseJson(raw, flag) : kind === "bool" ? raw === true : raw;
  }
  return obj;
}

async function writeExport(res, format, outputPath) {
  if (outputPath) {
    const buf = Buffer.from(await res.arrayBuffer());
    await writeFile(outputPath, buf);
    return out({ exported: outputPath, bytes: buf.length, format });
  }
  if (format === "xlsx") throw new Error("xlsx is binary; provide --output <path.xlsx>");
  const text = await res.text();
  process.stdout.write(text.endsWith("\n") ? text : `${text}\n`);
}

const HELP = `${CLI_NAME} ${PACKAGE_VERSION} — Metabase from the terminal (46 operations).

Auth (config-based; API key recommended):
  ${CLI_NAME} auth login --url <url> --api-key <key>
  ${CLI_NAME} auth login --url <url> --email <e> --password <p>
  ${CLI_NAME} auth status | auth logout
  Env (drop-in with the MCP): METABASE_URL, METABASE_API_KEY, METABASE_SESSION_TOKEN,
  METABASE_USER_EMAIL, METABASE_PASSWORD, METABASE_READ_ONLY. Global: --read-only.

Cards:      card list|get <id...>|create|update <id>|copy <id>|execute <id>|export <id>|metadata <id>|dashboards <id>|archive <id>
Dashboards: dashboard list|get <id...>|create|update <id>|copy <id>|set-cards <id> --cards <json>|archive <id>|metadata <id>
Databases:  database list|get <id>|metadata <id>|schemas <id>|schema-tables <id> <schema>
Tables:     table list|get <id...>|metadata <id>|fks <id>
Fields:     field get <id>|values <id>|update <id>
Queries:    query run <db-id> <sql>|export <db-id> <sql> --format csv|json|xlsx|to-native --mbql <json>
Collections: collection list|get <id|root>|items <id|root>|tree|create|update <id>
Discovery:  search [<q>] [--models a,b]|recent|whoami|cache invalidate [--database N] [--dashboard N]
History:    revision list <card|dashboard> <id>|revert <card|dashboard> <id> <revision-id>
Bookmarks:  bookmark add|remove <card|dashboard|collection> <id>

Output is optimized JSON. Use --output <path> for exports; xlsx requires --output.`;

async function client(values) {
  return auth.getClient(authOptions(values));
}

async function runAuth(sub, p, values) {
  switch (sub) {
    case "login": case undefined: return out(await auth.login(authOptions(values)));
    case "status": return out(await auth.status(authOptions(values)));
    case "logout": return out(await auth.logout());
    default: throw new Error(`Unknown auth subcommand: ${sub}`);
  }
}

async function runCard(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "list": return out(await api.listCards(c, { f: values.get("--f"), modelId: values.has("--model-id") ? int(values.get("--model-id"), "--model-id") : undefined }));
    case "get": return out(await api.getCard(c, ids(p, "card id")));
    case "create": return out(await api.createCard(c, {
      name: need(values.get("--name"), "--name"),
      dataset_query: requireJson(values, "--dataset-query"),
      display: need(values.get("--display"), "--display"),
      description: values.get("--description"),
      collection_id: values.has("--collection-id") ? int(values.get("--collection-id"), "--collection-id") : undefined,
      visualization_settings: json(values, "--visualization-settings"),
    }));
    case "update": return out(await api.updateCard(c, int(need(p[0], "card id"), "card id"), updates(values, [
      ["--name", "name"], ["--description", "description"], ["--display", "display"],
      ["--dataset-query", "dataset_query", "json"], ["--collection-id", "collection_id", "int"],
      ["--visualization-settings", "visualization_settings", "json"],
    ])));
    case "copy": return out(await api.copyCard(c, int(need(p[0], "card id"), "card id")));
    case "execute": return out(await api.executeCard(c, int(need(p[0], "card id"), "card id"), { parameters: json(values, "--parameters"), ignoreCache: values.get("--ignore-cache") === true }));
    case "export": return writeExport(await api.exportCardResults(c, int(need(p[0], "card id"), "card id"), need(values.get("--format"), "--format")), values.get("--format"), values.get("--output"));
    case "metadata": return out(await api.getCardMetadata(c, int(need(p[0], "card id"), "card id")));
    case "dashboards": return out(await api.listCardDashboards(c, int(need(p[0], "card id"), "card id")));
    case "archive": return out(await api.archiveCard(c, int(need(p[0], "card id"), "card id")));
    default: throw new Error(`Unknown card subcommand: ${sub}`);
  }
}

async function runDashboard(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "list": return out(await api.listDashboards(c, { f: values.get("--f") }));
    case "get": return out(await api.getDashboard(c, ids(p, "dashboard id")));
    case "create": return out(await api.createDashboard(c, {
      name: need(values.get("--name"), "--name"),
      description: values.get("--description"),
      collection_id: values.has("--collection-id") ? int(values.get("--collection-id"), "--collection-id") : undefined,
      parameters: json(values, "--parameters"),
    }));
    case "update": return out(await api.updateDashboard(c, int(need(p[0], "dashboard id"), "dashboard id"), updates(values, [
      ["--name", "name"], ["--description", "description"], ["--collection-id", "collection_id", "int"], ["--parameters", "parameters", "json"],
    ])));
    case "copy": return out(await api.copyDashboard(c, int(need(p[0], "dashboard id"), "dashboard id"), {
      name: values.get("--name"), description: values.get("--description"),
      collection_id: values.has("--collection-id") ? int(values.get("--collection-id"), "--collection-id") : undefined,
    }));
    case "set-cards": return out(await api.updateDashboardCards(c, int(need(p[0], "dashboard id"), "dashboard id"), requireJson(values, "--cards")));
    case "archive": return out(await api.archiveDashboard(c, int(need(p[0], "dashboard id"), "dashboard id")));
    case "metadata": return out(await api.getDashboardMetadata(c, int(need(p[0], "dashboard id"), "dashboard id")));
    default: throw new Error(`Unknown dashboard subcommand: ${sub}`);
  }
}

async function runDatabase(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "list": return out(await api.listDatabases(c, { includeCards: values.get("--include-cards") === true }));
    case "get": return out(await api.getDatabase(c, int(need(p[0], "database id"), "database id")));
    case "metadata": return out(await api.getDatabaseMetadata(c, int(need(p[0], "database id"), "database id")));
    case "schemas": return out(await api.listDatabaseSchemas(c, int(need(p[0], "database id"), "database id")));
    case "schema-tables": return out(await api.listSchemaTables(c, int(need(p[0], "database id"), "database id"), need(p[1], "schema")));
    default: throw new Error(`Unknown database subcommand: ${sub}`);
  }
}

async function runTable(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "list": return out(await api.listTables(c));
    case "get": return out(await api.getTable(c, ids(p, "table id")));
    case "metadata": return out(await api.getTableMetadata(c, int(need(p[0], "table id"), "table id"), { includeSensitiveFields: values.get("--include-sensitive-fields") === true }));
    case "fks": return out(await api.getTableFks(c, int(need(p[0], "table id"), "table id")));
    default: throw new Error(`Unknown table subcommand: ${sub}`);
  }
}

async function runField(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "get": return out(await api.getField(c, int(need(p[0], "field id"), "field id")));
    case "values": return out(await api.getFieldValues(c, int(need(p[0], "field id"), "field id")));
    case "update": return out(await api.updateField(c, int(need(p[0], "field id"), "field id"), updates(values, [
      ["--display-name", "display_name"], ["--description", "description"],
      ["--semantic-type", "semantic_type"], ["--visibility-type", "visibility_type"],
    ])));
    default: throw new Error(`Unknown field subcommand: ${sub}`);
  }
}

async function runQuery(sub, p, values) {
  const c = await client(values);
  switch (sub) {
    case "run": return out(await api.executeQuery(c, int(need(p[0], "database id"), "database id"), need(p[1] ?? values.get("--query"), "sql"), json(values, "--template-tags")));
    case "export": return writeExport(await api.exportQueryResults(c, int(need(p[0], "database id"), "database id"), need(p[1] ?? values.get("--query"), "sql"), need(values.get("--format"), "--format")), values.get("--format"), values.get("--output"));
    case "to-native": return out(await api.convertToNativeSql(c, requireJson(values, "--mbql")));
    default: throw new Error(`Unknown query subcommand: ${sub}`);
  }
}

async function runCollection(sub, p, values) {
  const c = await client(values);
  const cid = (label) => { const v = need(p[0], label); return v === "root" ? "root" : int(v, label); };
  switch (sub) {
    case "list": return out(await api.listCollections(c, { archived: values.get("--archived") === true }));
    case "get": return out(await api.getCollection(c, cid("collection id")));
    case "items": return out(await api.getCollectionItems(c, cid("collection id"), {
      models: list(values, "--models"),
      limit: values.has("--limit") ? int(values.get("--limit"), "--limit") : undefined,
      offset: values.has("--offset") ? int(values.get("--offset"), "--offset") : undefined,
    }));
    case "tree": return out(await api.getCollectionTree(c));
    case "create": return out(await api.createCollection(c, {
      name: need(values.get("--name"), "--name"),
      description: values.get("--description"),
      parent_id: values.has("--parent-id") ? int(values.get("--parent-id"), "--parent-id") : undefined,
      color: values.get("--color"),
    }));
    case "update": return out(await api.updateCollection(c, int(need(p[0], "collection id"), "collection id"), updates(values, [
      ["--name", "name"], ["--description", "description"], ["--color", "color"],
      ["--archived", "archived", "bool"], ["--parent-id", "parent_id", "int"],
    ])));
    default: throw new Error(`Unknown collection subcommand: ${sub}`);
  }
}

async function runRevision(sub, p, values) {
  const c = await client(values);
  const entity = need(p[0], "entity (card|dashboard)");
  switch (sub) {
    case "list": return out(await api.getRevisions(c, entity, int(need(p[1], "id"), "id")));
    case "revert": return out(await api.revertRevision(c, entity, int(need(p[1], "id"), "id"), int(need(p[2], "revision id"), "revision id")));
    default: throw new Error(`Unknown revision subcommand: ${sub}`);
  }
}

async function runBookmark(sub, p, values) {
  const c = await client(values);
  const model = need(p[0], "model (card|dashboard|collection)");
  const id = int(need(p[1], "id"), "id");
  if (sub === "add") return out(await api.toggleBookmark(c, model, id, "create"));
  if (sub === "remove") return out(await api.toggleBookmark(c, model, id, "delete"));
  throw new Error(`Unknown bookmark subcommand: ${sub}`);
}

async function main() {
  let parsed;
  try { parsed = parseArgs(process.argv.slice(2)); } catch (err) { return fail(err.message); }
  const { positionals, values } = parsed;

  if (values.get("--version") || values.get("-v")) return out({ name: CLI_NAME, version: PACKAGE_VERSION });
  const command = positionals[0];
  if (!command || command === "help" || values.get("--help") || values.get("-h")) {
    process.stdout.write(`${HELP}\n`);
    return;
  }
  const sub = positionals[1];
  const rest = positionals.slice(2);

  try {
    switch (command) {
      case "auth": return await runAuth(sub, rest, values);
      case "card": return await runCard(sub, rest, values);
      case "dashboard": return await runDashboard(sub, rest, values);
      case "database": return await runDatabase(sub, rest, values);
      case "table": return await runTable(sub, rest, values);
      case "field": return await runField(sub, rest, values);
      case "query": return await runQuery(sub, rest, values);
      case "collection": return await runCollection(sub, rest, values);
      case "search": return out(await api.search(await client(values), { q: sub, models: list(values, "--models"), archived: values.get("--archived") === true, limit: values.has("--limit") ? int(values.get("--limit"), "--limit") : undefined, offset: values.has("--offset") ? int(values.get("--offset"), "--offset") : undefined }));
      case "recent": return out(await api.getRecentViews(await client(values)));
      case "whoami": return out(await api.getCurrentUser(await client(values)));
      case "cache": {
        if (sub !== "invalidate") throw new Error(`Unknown cache subcommand: ${sub}`);
        return out(await api.invalidateCache(await client(values), { database: values.has("--database") ? int(values.get("--database"), "--database") : undefined, dashboard: values.has("--dashboard") ? int(values.get("--dashboard"), "--dashboard") : undefined }));
      }
      case "revision": return await runRevision(sub, rest, values);
      case "bookmark": return await runBookmark(sub, rest, values);
      default: return fail(`Unknown command: ${command}. Run \`${CLI_NAME} help\`.`);
    }
  } catch (err) {
    fail(err.message);
  }
}

main();
