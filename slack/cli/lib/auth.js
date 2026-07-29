// Credential storage and resolution for native-session (xoxc + xoxd) auth.
//
// Resolution order when a command needs credentials:
//   1. explicit options.token + options.cookie
//   2. env SLACK_TOKEN + SLACK_COOKIE
//   3. persisted ~/.config/slack-cli/credentials.json (team selected by
//      options.team, env SLACK_TEAM, persisted default, or first workspace)
//
// Enterprise Grid: the Slack desktop app holds one org-level token (team id
// `E…`, host `<org>.enterprise.slack.com`) alongside one token per workspace
// the user belongs to. The org-level token reaches the whole org for search,
// the user directory, and per-channel reads, but Slack refuses workspace-scoped
// methods such as conversations.list on it with `enterprise_is_restricted`.
// Resolution therefore hands every call the org token plus the sibling
// workspace tokens, so api.js can retry a refused call against a workspace and
// list channels across all of them.

import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { dirname } from "node:path";
import { CLI_NAME, CONFIG_ENV_KEYS, CREDENTIALS_FILE } from "./config.js";
import { extractCredentials } from "./extract.js";
import { webApiCall } from "./api.js";
import { discoverGridWorkspaces } from "./grid.js";

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

// Is this record the Enterprise Grid org-level entry rather than a workspace?
// Credentials stored before Grid support lack the flag, so fall back to the
// shape Slack gives org-level sessions: an `E…` team id on an enterprise host.
function isOrgLevel(workspace) {
  if (typeof workspace?.is_org_level === "boolean") return workspace.is_org_level;
  const host = hostFrom(workspace) || "";
  return Boolean(workspace?.id && String(workspace.id).startsWith("E")) || host.endsWith(".enterprise.slack.com");
}

function credentialsFor(store, workspace) {
  return {
    token: workspace.token,
    cookie: store.cookie,
    cookieDs: store.cookie_ds || null,
    host: hostFrom(workspace),
    team: workspace,
  };
}

// Workspace-level credentials the CLI can use when the org-level token is
// refused. `scopeTo` limits them to one Grid org; records written before Grid
// support carry no enterprise id, so they stay eligible rather than drop out.
function workspaceCredentials(store, scopeTo = null) {
  return (store?.workspaces || [])
    .filter((w) => w.token && !isOrgLevel(w))
    .filter((w) => !scopeTo || !w.enterprise_id || w.enterprise_id === scopeTo)
    .map((w) => credentialsFor(store, w));
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

// With nothing selected, prefer the Enterprise Grid org-level entry: it sees
// the whole org for search, the user directory, and channel reads, and the
// workspace tokens travel with it for the methods Slack scopes to a workspace.
function selectWorkspace(store, selector) {
  const workspaces = store?.workspaces || [];
  if (workspaces.length === 0) return null;
  return (
    matchWorkspace(workspaces, selector) ||
    matchWorkspace(workspaces, store?.default_team) ||
    workspaces.find(isOrgLevel) ||
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

// Verify one token against Slack and turn it into a workspace record.
async function verifyToken(token, cookie, cookieDs) {
  const who = await webApiCall("auth.test", {}, { token, cookie, cookieDs, host: null });
  const url = who.url ? who.url.replace(/\/+$/, "") : null;
  return {
    id: who.team_id || null,
    name: who.team || null,
    url,
    host: hostFromUrl(who.url),
    user_id: who.user_id || null,
    enterprise_id: who.enterprise_id || null,
    is_org_level: Boolean(who.is_enterprise_install) || String(who.team_id || "").startsWith("E"),
    token,
  };
}

// Turn raw xoxc tokens into verified workspace records by calling auth.test for
// each with the shared cookie. Invalid or stale tokens are dropped; duplicates
// for the same workspace collapse to one.
async function enrichTokens(tokens, cookie, cookieDs) {
  const byTeam = new Map();
  const errors = [];
  for (const token of tokens) {
    try {
      const record = await verifyToken(token, cookie, cookieDs);
      byTeam.set(record.id || record.url || token, record);
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
  // A token that failed while others succeeded used to vanish without a word.
  // Now that listings sweep every stored workspace, a dropped one subtracts its
  // channels from the result, so say which ones were skipped.
  const warnings = errors.length
    ? [
        `${errors.length} of ${tokens.length} cached token(s) failed verification and were skipped, so those workspaces are missing from every listing: ${errors.join("; ")}`,
      ]
    : [];
  return { workspaces, warnings };
}

// Resolve { token, cookie, cookieDs, host, team } for an API call.
export async function getCredentials(options = {}) {
  if (options.token && options.cookie) {
    return {
      token: options.token,
      cookie: options.cookie,
      cookieDs: options.cookieDs || null,
      host: options.host || null,
      team: null,
    };
  }

  const envToken = process.env[CONFIG_ENV_KEYS.token];
  const envCookie = process.env[CONFIG_ENV_KEYS.cookie];
  if (envToken && envCookie) {
    return {
      token: envToken,
      cookie: envCookie,
      cookieDs: process.env[CONFIG_ENV_KEYS.cookieDs] || null,
      host: options.host || null,
      team: null,
    };
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
  const orgLevel = isOrgLevel(workspace);
  return {
    ...credentialsFor(store, workspace),
    orgLevel,
    // Workspace tokens to retry against when Slack refuses the org-level token,
    // and the full set for listing channels across the whole org.
    fallbacks: orgLevel ? workspaceCredentials(store, workspace.enterprise_id || null) : [],
    teams: workspaceCredentials(store),
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
    ...(store.warnings?.length
      ? {
          incomplete: true,
          warnings: store.warnings,
          note: `Some workspaces could not be stored, so listings will not cover the whole org. Re-run \`${CLI_NAME} auth login\`.`,
        }
      : {}),
    workspaces: store.workspaces.map((w) => ({
      id: w.id,
      name: w.name,
      host: hostFrom(w),
      user_id: w.user_id,
      enterprise_id: w.enterprise_id || null,
      is_org_level: isOrgLevel(w),
    })),
  };
}

// Extract from the desktop app, verify each token, and persist. On Enterprise
// Grid the app often caches only the org-level token, so discovery mints the
// per-workspace tokens that workspace-scoped methods need.
export async function login(options = {}) {
  const { tokens, cookie, cookieDs } = extractCredentials();
  const verified = await enrichTokens(tokens, cookie, cookieDs);
  const discovered = await discoverGridWorkspaces(verified.workspaces, cookie, cookieDs, (token) =>
    verifyToken(token, cookie, cookieDs),
  );
  const { workspaces } = discovered;
  const warnings = [...verified.warnings, ...discovered.warnings];
  const store = {
    auth_type: "session",
    cookie,
    cookie_ds: cookieDs || null,
    workspaces,
    default_team:
      matchWorkspace(workspaces, options.team)?.id ||
      workspaces.find(isOrgLevel)?.id ||
      workspaces[0]?.id ||
      null,
    // Persisted so an incomplete login stays visible in `auth status` instead of
    // living only in the output of the command that produced it.
    warnings,
    stored_at: nowIso(),
  };
  await writeStore(store);

  const active = selectWorkspace(store, options.team);
  return {
    persisted: true,
    workspace_count: workspaces.length,
    active: { team: active.name, team_id: active.id, user_id: active.user_id, url: active.url },
    workspaces: workspaces.map((w) => ({ team: w.name, id: w.id, host: w.host, is_org_level: isOrgLevel(w) })),
    ...(warnings.length ? { warnings } : {}),
  };
}

// Persist credentials supplied by hand (token + cookie), bypassing extraction.
export async function importCredentials({ token, cookie, cookieDs, host, team } = {}) {
  if (!token || !token.startsWith("xoxc-")) {
    throw new Error("`--token` must be an xoxc- web token.");
  }
  if (!cookie) {
    throw new Error("`--cookie` is required (the xoxd- `d` cookie value).");
  }
  const cookieValue = cookie.startsWith("xoxd-") ? cookie : `xoxd-${cookie}`;
  const cookieDsValue = cookieDs || null;
  const resolvedHost = host || null;
  const verify = await webApiCall("auth.test", {}, { token, cookie: cookieValue, cookieDs: cookieDsValue, host: resolvedHost });

  const workspace = {
    id: verify.team_id || null,
    name: verify.team || team || null,
    url: verify.url ? verify.url.replace(/\/+$/, "") : null,
    host: verify.url ? new URL(verify.url).host : resolvedHost,
    user_id: verify.user_id || null,
    enterprise_id: verify.enterprise_id || null,
    is_org_level: Boolean(verify.is_enterprise_install) || String(verify.team_id || "").startsWith("E"),
    token,
  };
  // An imported Grid token names the rest of its org, so pick up the sibling
  // workspaces here too rather than leaving the import half-usable.
  const { workspaces, warnings } = await discoverGridWorkspaces([workspace], cookieValue, cookieDsValue, (t) =>
    verifyToken(t, cookieValue, cookieDsValue),
  );
  await writeStore({
    auth_type: "session",
    cookie: cookieValue,
    cookie_ds: cookieDsValue,
    workspaces,
    default_team: workspaces.find(isOrgLevel)?.id || workspace.id,
    warnings,
    stored_at: nowIso(),
  });
  return {
    persisted: true,
    verified: Boolean(verify.ok),
    active: { team: workspace.name, user: verify.user },
    workspace_count: workspaces.length,
    ...(warnings.length ? { warnings } : {}),
  };
}

// Parse token + cookie out of a cURL command copied from browser devtools.
export function parseCurl(curl) {
  if (typeof curl !== "string" || !curl.trim()) {
    throw new Error("Provide the cURL string copied from devtools.");
  }
  const tokenMatch = curl.match(/xoxc-[A-Za-z0-9-]+/);
  const cookieMatch = curl.match(/xoxd-[A-Za-z0-9%._-]+/);
  const dsMatch = curl.match(/d-s=([^;'"\s]+)/);
  const hostMatch = curl.match(/https?:\/\/([a-z0-9-]+\.slack\.com)/i);
  if (!tokenMatch) throw new Error("No xoxc- token found in the cURL command.");
  if (!cookieMatch) throw new Error("No xoxd- cookie found in the cURL command.");
  // Keep cookie values verbatim (percent-encoded as they travel on the wire);
  // decoding would corrupt the trailing `%3D` and Slack would reject it.
  return {
    token: tokenMatch[0],
    cookie: cookieMatch[0],
    cookieDs: dsMatch ? dsMatch[1] : null,
    host: hostMatch ? hostMatch[1] : null,
  };
}

export async function logout() {
  await clearCredentials();
  return { loggedOut: true };
}
