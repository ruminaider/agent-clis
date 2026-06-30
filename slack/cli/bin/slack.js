#!/usr/bin/env node

import { getAuthStatus, getCredentials, login, logout, importCredentials, parseCurl } from "../lib/auth.js";
import * as api from "../lib/api.js";
import { CLI_NAME, PACKAGE_VERSION } from "../lib/config.js";

const BOOLEAN_FLAGS = new Set([
  "--private",
  "--include-archived",
  "--inclusive",
  "--broadcast",
  "--json",
  "--help",
  "-h",
  "--version",
  "-v",
]);

const VALUE_FLAGS = new Set([
  "--token",
  "--cookie",
  "--cookie-ds",
  "--curl",
  "--host",
  "--team",
  "--channel",
  "--user",
  "--limit",
  "--cursor",
  "--oldest",
  "--latest",
  "--page",
  "--sort",
  "--sort-dir",
  "--types",
  "--ts",
  "--thread-ts",
  "--text",
  "--blocks",
  "--name",
  "--at",
  "--file",
]);

const ALL_FLAGS = new Set([...BOOLEAN_FLAGS, ...VALUE_FLAGS]);

function parseArgs(argv) {
  const positionals = [];
  const values = new Map();
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    // Everything after a bare `--` is positional, so message text may start
    // with a dash: `message send C -- "-1 from baseline"`.
    if (arg === "--") {
      positionals.push(...argv.slice(i + 1));
      break;
    }
    if (!arg.startsWith("-") || arg === "-") {
      positionals.push(arg);
      continue;
    }
    const eq = arg.indexOf("=");
    if (eq !== -1) {
      const flag = arg.slice(0, eq);
      if (!VALUE_FLAGS.has(flag)) throw new Error(`Unknown or non-value flag: ${flag}`);
      values.set(flag, arg.slice(eq + 1));
      continue;
    }
    if (BOOLEAN_FLAGS.has(arg)) {
      values.set(arg, true);
      continue;
    }
    if (VALUE_FLAGS.has(arg)) {
      const next = argv[i + 1];
      if (next === undefined || ALL_FLAGS.has(next)) throw new Error(`Flag ${arg} requires a value`);
      values.set(arg, next);
      i++;
      continue;
    }
    throw new Error(`Unknown flag: ${arg}`);
  }
  return { positionals, values };
}

function out(data) {
  process.stdout.write(`${JSON.stringify(data, null, 2)}\n`);
}

function fail(message, code = 1) {
  process.stderr.write(`${message}\n`);
  process.exit(code);
}

function need(value, label) {
  if (value === undefined || value === null || value === "") {
    throw new Error(`Missing required ${label}`);
  }
  return value;
}

const HELP = `${CLI_NAME} ${PACKAGE_VERSION} — Slack from the terminal, authenticated as you.

Auth (no app, no OAuth; uses your Slack desktop session):
  ${CLI_NAME} auth login              Extract + persist credentials from the Slack app
  ${CLI_NAME} auth status             Show authenticated workspaces
  ${CLI_NAME} auth import --curl '<curl>'        Import from a copied devtools cURL
  ${CLI_NAME} auth import --token xoxc-... --cookie xoxd-...
  ${CLI_NAME} auth logout

Channels:
  ${CLI_NAME} channel list [--types ...] [--limit N] [--include-archived]
  ${CLI_NAME} channel info <channel>
  ${CLI_NAME} channel history <channel> [--limit N] [--oldest ts] [--latest ts]
  ${CLI_NAME} channel members <channel>
  ${CLI_NAME} channel join <channel>
  ${CLI_NAME} channel create <name> [--private]

Messages:
  ${CLI_NAME} message send <channel> <text> [--thread-ts ts] [--broadcast]
  ${CLI_NAME} message reply <channel> <thread-ts> <text>
  ${CLI_NAME} message update <channel> <ts> <text>
  ${CLI_NAME} message delete <channel> <ts>
  ${CLI_NAME} message schedule <channel> <text> --at <unix-ts>
  ${CLI_NAME} thread read <channel> <thread-ts>

Search:
  ${CLI_NAME} search messages <query> [--limit N] [--sort score|timestamp]
  ${CLI_NAME} search files <query>
  ${CLI_NAME} search all <query>

People & reactions:
  ${CLI_NAME} user list | user info <user> | user me
  ${CLI_NAME} reaction add <channel> <ts> <emoji>
  ${CLI_NAME} reaction remove <channel> <ts> <emoji>

Files, pins, canvases:
  ${CLI_NAME} file list [--channel C] | file info <file>
  ${CLI_NAME} pin list <channel> | pin add <channel> <ts> | pin remove <channel> <ts>
  ${CLI_NAME} canvas list [--channel C] | canvas get <canvas-id>

Global: --team <name|id|host> to target a workspace. Output is JSON.
Text starting with a dash: put it after a bare '--', e.g. message send C0123 -- "-1 vs baseline".`;

async function creds(values) {
  return getCredentials({
    token: values.get("--token"),
    cookie: values.get("--cookie"),
    cookieDs: values.get("--cookie-ds"),
    host: values.get("--host"),
    team: values.get("--team"),
  });
}

async function runAuth(sub, positionals, values) {
  switch (sub) {
    case "login":
    case undefined:
      return out(await login({ team: values.get("--team") }));
    case "status":
      return out(await getAuthStatus());
    case "logout":
      return out(await logout());
    case "import": {
      if (values.get("--curl")) {
        const parsed = parseCurl(values.get("--curl"));
        return out(await importCredentials({
          ...parsed,
          cookieDs: values.get("--cookie-ds") || parsed.cookieDs,
          host: values.get("--host") || parsed.host,
        }));
      }
      return out(await importCredentials({
        token: values.get("--token"),
        cookie: values.get("--cookie"),
        cookieDs: values.get("--cookie-ds"),
        host: values.get("--host"),
        team: values.get("--team"),
      }));
    }
    default:
      throw new Error(`Unknown auth subcommand: ${sub}`);
  }
}

async function runChannel(sub, p, values) {
  const c = await creds(values);
  const opts = {
    types: values.get("--types"),
    limit: values.get("--limit"),
    cursor: values.get("--cursor"),
    oldest: values.get("--oldest"),
    latest: values.get("--latest"),
    inclusive: values.get("--inclusive"),
    includeArchived: values.get("--include-archived"),
    private: values.get("--private"),
  };
  switch (sub) {
    case "list": return out(await api.channelList(c, opts));
    case "info": return out(await api.channelInfo(c, need(p[0] || values.get("--channel"), "channel")));
    case "history": return out(await api.channelHistory(c, need(p[0] || values.get("--channel"), "channel"), opts));
    case "members": return out(await api.channelMembers(c, need(p[0] || values.get("--channel"), "channel"), opts));
    case "join": return out(await api.channelJoin(c, need(p[0] || values.get("--channel"), "channel")));
    case "create": return out(await api.channelCreate(c, need(p[0] || values.get("--name"), "name"), opts));
    default: throw new Error(`Unknown channel subcommand: ${sub}`);
  }
}

async function runMessage(sub, p, values) {
  const c = await creds(values);
  const channel = () => need(p[0] || values.get("--channel"), "channel");
  switch (sub) {
    case "send":
      return out(await api.messageSend(c, channel(), need(p[1] ?? values.get("--text"), "text"), {
        threadTs: values.get("--thread-ts"),
        broadcast: values.get("--broadcast"),
        blocks: values.get("--blocks"),
      }));
    case "reply":
      return out(await api.messageSend(c, channel(), need(p[2] ?? values.get("--text"), "text"), {
        threadTs: need(p[1] || values.get("--thread-ts"), "thread-ts"),
        broadcast: values.get("--broadcast"),
      }));
    case "update":
      return out(await api.messageUpdate(c, channel(), need(p[1] || values.get("--ts"), "ts"), need(p[2] ?? values.get("--text"), "text"), {
        blocks: values.get("--blocks"),
      }));
    case "delete":
      return out(await api.messageDelete(c, channel(), need(p[1] || values.get("--ts"), "ts")));
    case "schedule":
      return out(await api.messageSchedule(c, channel(), need(p[1] ?? values.get("--text"), "text"), need(values.get("--at"), "--at"), {
        threadTs: values.get("--thread-ts"),
      }));
    default: throw new Error(`Unknown message subcommand: ${sub}`);
  }
}

async function runThread(sub, p, values) {
  const c = await creds(values);
  if (sub !== "read") throw new Error(`Unknown thread subcommand: ${sub}`);
  return out(await api.threadRead(c, need(p[0] || values.get("--channel"), "channel"), need(p[1] || values.get("--thread-ts"), "thread-ts"), {
    limit: values.get("--limit"),
    cursor: values.get("--cursor"),
  }));
}

async function runSearch(sub, p, values) {
  const c = await creds(values);
  const query = need(p[0] || values.get("--text"), "query");
  const opts = { limit: values.get("--limit"), page: values.get("--page"), sort: values.get("--sort"), sortDir: values.get("--sort-dir") };
  switch (sub) {
    case "messages": return out(await api.searchMessages(c, query, opts));
    case "files": return out(await api.searchFiles(c, query, opts));
    case "all": return out(await api.searchAll(c, query, opts));
    default: throw new Error(`Unknown search subcommand: ${sub}`);
  }
}

async function runUser(sub, p, values) {
  const c = await creds(values);
  switch (sub) {
    case "list": return out(await api.userList(c, { limit: values.get("--limit"), cursor: values.get("--cursor") }));
    case "info": return out(await api.userInfo(c, need(p[0] || values.get("--user"), "user")));
    case "me": {
      const who = await api.authTest(c);
      return out(await api.userInfo(c, who.user_id));
    }
    default: throw new Error(`Unknown user subcommand: ${sub}`);
  }
}

async function runReaction(sub, p, values) {
  const c = await creds(values);
  const channel = need(p[0] || values.get("--channel"), "channel");
  const ts = need(p[1] || values.get("--ts"), "ts");
  const name = need(p[2] || values.get("--name"), "emoji");
  if (sub === "add") return out(await api.reactionAdd(c, channel, ts, name));
  if (sub === "remove") return out(await api.reactionRemove(c, channel, ts, name));
  throw new Error(`Unknown reaction subcommand: ${sub}`);
}

async function runFile(sub, p, values) {
  const c = await creds(values);
  switch (sub) {
    case "list": return out(await api.fileList(c, { channel: values.get("--channel"), user: values.get("--user"), limit: values.get("--limit"), page: values.get("--page") }));
    case "info": return out(await api.fileInfo(c, need(p[0] || values.get("--file"), "file")));
    default: throw new Error(`Unknown file subcommand: ${sub}`);
  }
}

async function runPin(sub, p, values) {
  const c = await creds(values);
  const channel = need(p[0] || values.get("--channel"), "channel");
  switch (sub) {
    case "list": return out(await api.pinList(c, channel));
    case "add": return out(await api.pinAdd(c, channel, need(p[1] || values.get("--ts"), "ts")));
    case "remove": return out(await api.pinRemove(c, channel, need(p[1] || values.get("--ts"), "ts")));
    default: throw new Error(`Unknown pin subcommand: ${sub}`);
  }
}

async function runCanvas(sub, p, values) {
  const c = await creds(values);
  if (sub === "list") return out(await api.canvasList(c, { channel: values.get("--channel"), limit: values.get("--limit") }));
  if (sub === "get") return out(await api.canvasGet(c, need(p[0], "canvas-id")));
  throw new Error(`Unknown canvas subcommand: ${sub}`);
}

async function main() {
  const argv = process.argv.slice(2);
  let parsed;
  try {
    parsed = parseArgs(argv);
  } catch (err) {
    fail(err.message);
  }
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
      case "channel": return await runChannel(sub, rest, values);
      case "message": return await runMessage(sub, rest, values);
      case "thread": return await runThread(sub, rest, values);
      case "search": return await runSearch(sub, rest, values);
      case "user": return await runUser(sub, rest, values);
      case "reaction": return await runReaction(sub, rest, values);
      case "file": return await runFile(sub, rest, values);
      case "pin": return await runPin(sub, rest, values);
      case "canvas": return await runCanvas(sub, rest, values);
      default:
        fail(`Unknown command: ${command}. Run \`${CLI_NAME} help\`.`);
    }
  } catch (err) {
    fail(err.message);
  }
}

main();
