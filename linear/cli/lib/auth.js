import { createServer } from "node:http";
import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { randomBytes, createHash } from "node:crypto";
import { dirname } from "node:path";
import {
  CLI_NAME,
  CREDENTIALS_FILE,
  DEFAULT_AUTH_PORT,
  OAUTH_AUTHORIZATION_SERVER_URL,
  OAUTH_PROTECTED_RESOURCE_URL,
  OAUTH_AUTHORIZE_URL,
  OAUTH_REGISTER_URL,
  OAUTH_REVOCATION_URL,
  OAUTH_TOKEN_URL,
  PACKAGE_NAME,
  TOOL_NAME,
} from "./config.js";

const CREDENTIALS_DIR = dirname(CREDENTIALS_FILE);
const REFRESH_SKEW_MS = 60_000;
const LOGIN_TIMEOUT_MS = 5 * 60 * 1000;

function nowIso() {
  return new Date().toISOString();
}

function credentialKind(creds) {
  if (!creds) return "none";
  if (creds.apiKey) return "api-key";
  if (creds.access_token) return "oauth";
  return "none";
}

function isExpired(creds) {
  return Boolean(creds?.expires_at && Date.now() > creds.expires_at - REFRESH_SKEW_MS);
}

function normalizeCredentials(creds) {
  if (!creds || typeof creds !== "object") return null;
  return {
    ...creds,
    auth_type: creds.auth_type || credentialKind(creds),
    stored_at: creds.stored_at || nowIso(),
  };
}

async function readJson(url) {
  const res = await fetch(url, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    throw new Error(`Discovery failed for ${url}: ${res.status} ${await res.text()}`);
  }
  return res.json();
}

async function discoverOAuthMetadata() {
  const [resourceMeta, serverMeta] = await Promise.all([
    readJson(OAUTH_PROTECTED_RESOURCE_URL).catch(() => null),
    readJson(OAUTH_AUTHORIZATION_SERVER_URL).catch(() => null),
  ]);

  const server = serverMeta || resourceMeta || {};
  const resource = resourceMeta || {};

  return {
    authorizationEndpoint: server.authorization_endpoint || resource.authorization_endpoint || OAUTH_AUTHORIZE_URL,
    tokenEndpoint: server.token_endpoint || resource.token_endpoint || OAUTH_TOKEN_URL,
    registrationEndpoint: server.registration_endpoint || resource.registration_endpoint || OAUTH_REGISTER_URL,
    revocationEndpoint: server.revocation_endpoint || resource.revocation_endpoint || OAUTH_REVOCATION_URL,
    issuer: server.issuer || resource.issuer || null,
    resource,
    server,
  };
}

async function loadCredentials() {
  try {
    return normalizeCredentials(JSON.parse(await readFile(CREDENTIALS_FILE, "utf8")));
  } catch {
    return null;
  }
}

async function saveCredentials(creds) {
  await mkdir(CREDENTIALS_DIR, { recursive: true });
  await writeFile(CREDENTIALS_FILE, `${JSON.stringify(normalizeCredentials(creds), null, 2)}\n`, {
    mode: 0o600,
  });
}

async function clearCredentials() {
  try {
    await unlink(CREDENTIALS_FILE);
  } catch (err) {
    if (err?.code !== "ENOENT") throw err;
  }
}

async function getAuthStatus(options = {}) {
  const explicitApiKey = options.apiKey || null;
  if (explicitApiKey) {
    return { authenticated: true, authType: "api-key", source: "input" };
  }

  const envApiKey = process.env.LINEAR_API_KEY || null;
  if (envApiKey) {
    return { authenticated: true, authType: "api-key", source: "env" };
  }

  const creds = await loadCredentials();
  if (!creds) {
    return { authenticated: false, authType: "none", source: "missing" };
  }

  if (creds.apiKey) {
    return { authenticated: true, authType: "api-key", source: "persisted", expiresSoon: false };
  }

  if (creds.access_token) {
    return {
      authenticated: true,
      authType: "oauth",
      source: "persisted",
      expiresSoon: isExpired(creds),
      canRefresh: Boolean(creds.refresh_token),
    };
  }

  return { authenticated: false, authType: "none", source: "invalid" };
}

async function registerClient(redirectUri, metadata) {
  const res = await fetch(metadata.registrationEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/json", Accept: "application/json" },
    body: JSON.stringify({
      client_name: `${PACKAGE_NAME} (${TOOL_NAME})`,
      redirect_uris: [redirectUri],
      grant_types: ["authorization_code", "refresh_token"],
      response_types: ["code"],
      token_endpoint_auth_method: "none",
    }),
  });

  if (!res.ok) {
    throw new Error(`Client registration failed: ${res.status} ${await res.text()}`);
  }

  return res.json();
}

function generatePKCE() {
  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  return { verifier, challenge };
}

async function exchangeToken(tokenEndpoint, params, errorLabel) {
  const res = await fetch(tokenEndpoint, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
    body: new URLSearchParams(params),
  });
  if (!res.ok) {
    throw new Error(`${errorLabel} failed: ${res.status} ${await res.text()}`);
  }
  return res.json();
}

async function refreshToken(creds) {
  const metadata = await discoverOAuthMetadata();
  const data = await exchangeToken(metadata.tokenEndpoint, {
    grant_type: "refresh_token",
    refresh_token: creds.refresh_token,
    client_id: creds.client_id,
  }, "Token refresh");

  const updated = normalizeCredentials({
    ...creds,
    access_token: data.access_token,
    refresh_token: data.refresh_token || creds.refresh_token,
    token_type: data.token_type || creds.token_type,
    expires_at: data.expires_in ? Date.now() + data.expires_in * 1000 : creds.expires_at,
    refreshed_at: nowIso(),
  });

  await saveCredentials(updated);
  return updated;
}

async function persistApiKey(apiKey, source = "input") {
  const creds = normalizeCredentials({
    apiKey,
    auth_type: "api-key",
    api_key_source: source,
    obtained_at: nowIso(),
  });
  await saveCredentials(creds);
  return creds;
}

async function login(options = {}) {
  if (options.apiKey) {
    return persistApiKey(options.apiKey, "input");
  }

  const port = options.port || DEFAULT_AUTH_PORT;
  const redirectUri = options.redirectUri || `http://127.0.0.1:${port}/callback`;
  const metadata = await discoverOAuthMetadata();

  console.log("Registering OAuth client with Linear...");
  const client = await registerClient(redirectUri, metadata);
  const clientId = client.client_id;
  const { verifier, challenge } = generatePKCE();
  const state = randomBytes(16).toString("hex");

  return new Promise((resolve, reject) => {
    let timeoutHandle = null;
    const finish = (fn, value) => {
      if (timeoutHandle) clearTimeout(timeoutHandle);
      server.close();
      fn(value);
    };

    const server = createServer(async (req, res) => {
      try {
        const url = new URL(req.url, redirectUri);
        if (url.pathname !== "/callback") {
          res.writeHead(404);
          res.end("Not found");
          return;
        }

        const error = url.searchParams.get("error");
        if (error) {
          const desc = url.searchParams.get("error_description") || error;
          res.writeHead(200, { "Content-Type": "text/html" });
          res.end(html("Authorization Failed", `<p>${escapeHtml(desc)}</p>`));
          finish(reject, new Error(`OAuth error: ${desc}`));
          return;
        }

        const code = url.searchParams.get("code");
        const returnedState = url.searchParams.get("state");
        if (!code) throw new Error("No authorization code received");
        if (returnedState !== state) throw new Error("OAuth state mismatch");

        const tokenData = await exchangeToken(metadata.tokenEndpoint, {
          grant_type: "authorization_code",
          code,
          redirect_uri: redirectUri,
          client_id: clientId,
          code_verifier: verifier,
        }, "Token exchange");

        const credentials = normalizeCredentials({
          access_token: tokenData.access_token,
          refresh_token: tokenData.refresh_token,
          token_type: tokenData.token_type,
          expires_at: tokenData.expires_in ? Date.now() + tokenData.expires_in * 1000 : undefined,
          client_id: clientId,
          issuer: metadata.issuer,
          obtained_at: nowIso(),
        });

        await saveCredentials(credentials);
        res.writeHead(200, { "Content-Type": "text/html" });
        res.end(html("✓ Authenticated with Linear", "<p>You can close this tab and return to the terminal.</p>"));
        finish(resolve, credentials);
      } catch (err) {
        res.writeHead(500, { "Content-Type": "text/html" });
        res.end(html("Token Exchange Failed", `<p>${escapeHtml(err.message)}</p>`));
        finish(reject, err);
      }
    });

    server.listen(port, async () => {
      const authUrl = new URL(metadata.authorizationEndpoint);
      authUrl.searchParams.set("client_id", clientId);
      authUrl.searchParams.set("response_type", "code");
      authUrl.searchParams.set("redirect_uri", redirectUri);
      authUrl.searchParams.set("state", state);
      authUrl.searchParams.set("code_challenge", challenge);
      authUrl.searchParams.set("code_challenge_method", "S256");

      console.log(`\nOpening browser for Linear authorization...`);
      console.log(`If browser doesn't open, visit:\n${authUrl.toString()}\n`);

      try {
        const open = (await import("open")).default;
        await open(authUrl.toString());
      } catch (error) {
        console.log("Browser auto-open failed. Open the URL above manually.");
        if (error instanceof Error && error.message) {
          console.log(`Auto-open error: ${error.message}`);
        }
      }

      console.log("Waiting for authorization...");
    });

    timeoutHandle = setTimeout(() => {
      timeoutHandle = null;
      server.close();
      reject(new Error(`Authorization timed out after ${LOGIN_TIMEOUT_MS / 60000} minutes`));
    }, LOGIN_TIMEOUT_MS);
    timeoutHandle.unref?.();
  });
}

async function logout() {
  await clearCredentials();
  return { loggedOut: true };
}

async function getAccessToken(options = {}) {
  if (options.apiKey) return options.apiKey;
  if (process.env.LINEAR_API_KEY) return process.env.LINEAR_API_KEY;

  const creds = await loadCredentials();
  if (!creds) {
    throw new Error(`Not authenticated. Run: ${CLI_NAME} auth login`);
  }

  if (creds.apiKey) return creds.apiKey;

  if (!creds.access_token) {
    throw new Error(`Invalid credentials. Run: ${CLI_NAME} auth login`);
  }

  if (creds.refresh_token && isExpired(creds)) {
    try {
      const refreshed = await refreshToken(creds);
      return refreshed.access_token;
    } catch (err) {
      throw new Error(`Token refresh failed: ${err.message}. Run: ${CLI_NAME} auth login`);
    }
  }

  return creds.access_token;
}

function escapeHtml(value) {
  return String(value)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

function html(title, body) {
  return `<!DOCTYPE html><html><head><title>${title}</title>
<style>body{font-family:system-ui;max-width:480px;margin:80px auto;text-align:center;}</style>
</head><body><h1>${title}</h1>${body}</body></html>`;
}

export {
  discoverOAuthMetadata,
  getAuthStatus,
  loadCredentials,
  saveCredentials,
  clearCredentials,
  refreshToken,
  login,
  logout,
  getAccessToken,
};
