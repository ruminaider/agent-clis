// Credential resolution and storage for metabase-cli.
//
// Resolution order for every field: explicit CLI options, then environment
// (the same names the metabase-mcp-server uses), then the persisted config at
// ~/.config/metabase-cli/credentials.json.

import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { dirname } from "node:path";
import { CLI_NAME, CREDENTIALS_FILE, ENV } from "./config.js";
import { MetabaseClient, MetabaseError } from "./client.js";

const DIR = dirname(CREDENTIALS_FILE);

function nowIso() {
  return new Date().toISOString();
}

function trimUrl(url) {
  return typeof url === "string" ? url.replace(/\/+$/, "") : url;
}

async function loadStore() {
  try {
    return JSON.parse(await readFile(CREDENTIALS_FILE, "utf8"));
  } catch {
    return null;
  }
}

async function saveStore(store) {
  await mkdir(DIR, { recursive: true });
  await writeFile(CREDENTIALS_FILE, `${JSON.stringify(store, null, 2)}\n`, { mode: 0o600 });
}

export async function logout() {
  try {
    await unlink(CREDENTIALS_FILE);
  } catch (err) {
    if (err?.code !== "ENOENT") throw err;
  }
  return { loggedOut: true };
}

// Build the credential object the client needs, from options -> env -> store.
export async function resolveCreds(options = {}) {
  const store = await loadStore();

  const url = trimUrl(options.url || process.env[ENV.url] || store?.url);
  if (!url) {
    throw new Error(`No Metabase URL. Run: ${CLI_NAME} auth login --url <url> --api-key <key>`);
  }

  const readOnly =
    options.readOnly === true ||
    process.env[ENV.readOnly] === "true" ||
    store?.read_only === true;

  const apiKey = options.apiKey || process.env[ENV.apiKey] || store?.api_key;
  const sessionToken = options.sessionToken || process.env[ENV.sessionToken] || store?.session_token;
  const email = options.email || process.env[ENV.email] || store?.email;
  const password = options.password || process.env[ENV.password] || store?.password;

  let authMethod;
  if (apiKey) authMethod = "api-key";
  else if (sessionToken) authMethod = "session-token";
  else if (email) {
    if (!password) throw new Error("A password is required with an email login.");
    authMethod = "email-password";
  } else {
    throw new Error(
      `No credentials. Provide --api-key (recommended), --session-token, or --email + --password, or run: ${CLI_NAME} auth login`,
    );
  }

  return { url, authMethod, apiKey, sessionToken, email, password, readOnly };
}

export async function getClient(options = {}) {
  return new MetabaseClient(await resolveCreds(options));
}

// Verify credentials and persist them. Never stores or prints more than needed.
export async function login(options = {}) {
  const creds = await resolveCreds(options);
  const client = new MetabaseClient(creds);
  let who;
  try {
    who = await client.get("/api/user/current");
  } catch (err) {
    if (err instanceof MetabaseError && err.status === 401) {
      throw new Error("Authentication failed: check the URL and credentials.");
    }
    throw err;
  }

  const store = {
    url: creds.url,
    auth_method: creds.authMethod,
    read_only: creds.readOnly,
    stored_at: nowIso(),
  };
  if (creds.authMethod === "api-key") store.api_key = creds.apiKey;
  else if (creds.authMethod === "session-token") store.session_token = creds.sessionToken;
  else if (creds.authMethod === "email-password") {
    store.email = creds.email;
    store.password = creds.password;
  }
  await saveStore(store);

  return {
    persisted: true,
    url: creds.url,
    auth_method: creds.authMethod,
    read_only: creds.readOnly,
    user: { id: who?.id, email: who?.email, name: who?.common_name || null, is_superuser: who?.is_superuser },
  };
}

export async function status(options = {}) {
  let creds;
  try {
    creds = await resolveCreds(options);
  } catch (err) {
    return { authenticated: false, reason: err.message };
  }
  try {
    const who = await new MetabaseClient(creds).get("/api/user/current");
    return {
      authenticated: true,
      url: creds.url,
      auth_method: creds.authMethod,
      read_only: creds.readOnly,
      user: { id: who?.id, email: who?.email, name: who?.common_name || null },
    };
  } catch (err) {
    return { authenticated: false, url: creds.url, auth_method: creds.authMethod, reason: err.message };
  }
}
