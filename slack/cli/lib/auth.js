// Credential storage and resolution for native-session (xoxc + xoxd) auth.
//
// Resolution order when a command needs credentials:
//   1. explicit options.token + options.cookie
//   2. env SLACK_TOKEN + SLACK_COOKIE
//   3. persisted ~/.config/slack-cli/credentials.json (team selected by
//      options.team, env SLACK_TEAM, persisted default, or first workspace)

import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { dirname } from "node:path";
import { CLI_NAME, CONFIG_ENV_KEYS, CREDENTIALS_FILE } from "./config.js";
import { extractCredentials } from "./extract.js";
import { webApiCall } from "./api.js";

const CREDENTIALS_DIR = dirname(CREDENTIALS_FILE);

function nowIso() {
  return new Date().toISOString();
}

async function readStore() {
  try {
    return JSON.parse(await readFile(CREDENTIALS_FILE, "utf8"));
  } catch {
    return null;
  }
}

async function writeStore(store) {
  await mkdir(CREDENTIALS_DIR, { recursive: true });
  await writeFile(CREDENTIALS_FILE, `${JSON.stringify(store, null, 2)}\n`, { mode: 0o600 });
}

export async function clearCredentials() {
  try {
    await unlink(CREDENTIALS_FILE);
  } catch (err) {
    if (err?.code !== "ENOENT") throw err;
  }
}

function hostFrom(workspace) {
  return workspace?.host || (workspace?.url ? workspace.url.replace(/^https?:\/\//, "").split("/")[0] : null);
}

function matchWorkspace(workspaces, selector) {
  if (!selector || !Array.isArray(workspaces)) return null;
  const needle = String(selector).toLowerCase().replace(/^https?:\/\//, "").replace(/\/+$/, "");
  return (
    workspaces.find((w) => w.id && String(w.id).toLowerCase() === needle) ||
    workspaces.find((w) => w.name && w.name.toLowerCase() === needle) ||
    workspaces.find((w) => hostFrom(w) && hostFrom(w).toLowerCase() === needle) ||
    workspaces.find((w) => hostFrom(w) && hostFrom(w).toLowerCase().startsWith(`${needle}.`)) ||
    null
  );
}

function selectWorkspace(store, selector) {
  const workspaces = store?.workspaces || [];
  if (workspaces.length === 0) return null;
  return (
    matchWorkspace(workspaces, selector) ||
    matchWorkspace(workspaces, store?.default_team) ||
    workspaces[0]
  );
}

function hostFromUrl(url) {
  if (!url) return null;
  try {
    return new URL(url).host;
  } catch {
    return url.replace(/^https?:\/\//, "").split("/")[0] || null;
  }
}

// Turn raw xoxc tokens into verified workspace records by calling auth.test for
// each with the shared cookie. Invalid or stale tokens are dropped; duplicates
// for the same workspace collapse to one.
async function enrichTokens(tokens, cookie) {
  const byTeam = new Map();
  const errors = [];
  for (const token of tokens) {
    try {
      const who = await webApiCall("auth.test", {}, { token, cookie, host: null });
      const url = who.url ? who.url.replace(/\/+$/, "") : null;
      byTeam.set(who.team_id || url || token, {
        id: who.team_id || null,
        name: who.team || null,
        url,
        host: hostFromUrl(who.url),
        user_id: who.user_id || null,
        token,
      });
    } catch (err) {
      errors.push(err.message);
    }
  }
  const workspaces = [...byTeam.values()];
  if (workspaces.length === 0) {
    throw new Error(
      `Found ${tokens.length} token(s) but none verified against Slack${
        errors.length ? ` (last error: ${errors[errors.length - 1]})` : ""
      }. The session may have expired; reopen Slack or use \`slack-cli auth import\`.`,
    );
  }
  return workspaces;
}

// Resolve { token, cookie, host, team } for an API call.
export async function getCredentials(options = {}) {
  if (options.token && options.cookie) {
    return {
      token: options.token,
      cookie: options.cookie,
      host: options.host || null,
      team: null,
    };
  }

  const envToken = process.env[CONFIG_ENV_KEYS.token];
  const envCookie = process.env[CONFIG_ENV_KEYS.cookie];
  if (envToken && envCookie) {
    return { token: envToken, cookie: envCookie, host: options.host || null, team: null };
  }

  const store = await readStore();
  if (!store?.cookie || !store?.workspaces?.length) {
    throw new Error(`Not authenticated. Run: ${CLI_NAME} auth login`);
  }
  const selector = options.team || process.env[CONFIG_ENV_KEYS.team];
  const workspace = selectWorkspace(store, selector);
  if (!workspace?.token) {
    throw new Error(`No matching Slack workspace. Run: ${CLI_NAME} auth status`);
  }
  return {
    token: workspace.token,
    cookie: store.cookie,
    host: hostFrom(workspace),
    team: workspace,
  };
}

export async function getAuthStatus() {
  const envToken = process.env[CONFIG_ENV_KEYS.token];
  const envCookie = process.env[CONFIG_ENV_KEYS.cookie];
  if (envToken && envCookie) {
    return { authenticated: true, source: "env", workspaces: [] };
  }
  const store = await readStore();
  if (!store?.cookie || !store?.workspaces?.length) {
    return { authenticated: false, source: "missing", workspaces: [] };
  }
  return {
    authenticated: true,
    source: "persisted",
    default_team: store.default_team || null,
    stored_at: store.stored_at || null,
    workspaces: store.workspaces.map((w) => ({
      id: w.id,
      name: w.name,
      host: hostFrom(w),
      user_id: w.user_id,
    })),
  };
}

// Extract from the desktop app, verify each token, and persist.
export async function login(options = {}) {
  const { tokens, cookie } = extractCredentials();
  const workspaces = await enrichTokens(tokens, cookie);
  const store = {
    auth_type: "session",
    cookie,
    workspaces,
    default_team: matchWorkspace(workspaces, options.team)?.id || workspaces[0]?.id || null,
    stored_at: nowIso(),
  };
  await writeStore(store);

  const active = selectWorkspace(store, options.team);
  return {
    persisted: true,
    workspace_count: workspaces.length,
    active: { team: active.name, team_id: active.id, user_id: active.user_id, url: active.url },
    workspaces: workspaces.map((w) => ({ team: w.name, id: w.id, host: w.host })),
  };
}

// Persist credentials supplied by hand (token + cookie), bypassing extraction.
export async function importCredentials({ token, cookie, host, team } = {}) {
  if (!token || !token.startsWith("xoxc-")) {
    throw new Error("`--token` must be an xoxc- web token.");
  }
  if (!cookie) {
    throw new Error("`--cookie` is required (the xoxd- `d` cookie value).");
  }
  const cookieValue = cookie.startsWith("xoxd-") ? cookie : `xoxd-${cookie}`;
  const resolvedHost = host || null;
  const verify = await webApiCall("auth.test", {}, { token, cookie: cookieValue, host: resolvedHost });

  const workspace = {
    id: verify.team_id || null,
    name: verify.team || team || null,
    url: verify.url ? verify.url.replace(/\/+$/, "") : null,
    host: verify.url ? new URL(verify.url).host : resolvedHost,
    user_id: verify.user_id || null,
    token,
  };
  await writeStore({
    auth_type: "session",
    cookie: cookieValue,
    workspaces: [workspace],
    default_team: workspace.id,
    stored_at: nowIso(),
  });
  return { persisted: true, verified: Boolean(verify.ok), active: { team: workspace.name, user: verify.user } };
}

// Parse token + cookie out of a cURL command copied from browser devtools.
export function parseCurl(curl) {
  if (typeof curl !== "string" || !curl.trim()) {
    throw new Error("Provide the cURL string copied from devtools.");
  }
  const tokenMatch = curl.match(/xoxc-[A-Za-z0-9-]+/);
  const cookieMatch = curl.match(/xoxd-[A-Za-z0-9%._-]+/);
  const hostMatch = curl.match(/https?:\/\/([a-z0-9-]+\.slack\.com)/i);
  if (!tokenMatch) throw new Error("No xoxc- token found in the cURL command.");
  if (!cookieMatch) throw new Error("No xoxd- cookie found in the cURL command.");
  // Keep the cookie value verbatim (percent-encoded as it travels on the wire);
  // decoding it would corrupt the trailing `%3D` and Slack would reject it.
  return {
    token: tokenMatch[0],
    cookie: cookieMatch[0],
    host: hostMatch ? hostMatch[1] : null,
  };
}

export async function logout() {
  await clearCredentials();
  return { loggedOut: true };
}
