import { createServer } from "node:http";
import { readFile, writeFile, mkdir, unlink } from "node:fs/promises";
import { homedir } from "node:os";
import { join } from "node:path";
import { randomBytes, createHash } from "node:crypto";

const CONFIG_DIR = join(homedir(), ".config", "notion-cli-tool");
const CREDENTIALS_FILE = join(CONFIG_DIR, "credentials.json");

// Notion MCP OAuth endpoints (discovered from .well-known)
const MCP_BASE = "https://mcp.notion.com";
const REGISTER_URL = `${MCP_BASE}/register`;
const AUTHORIZE_URL = `${MCP_BASE}/authorize`;
const TOKEN_URL = `${MCP_BASE}/token`;

// ─── Credential Storage ──────────────────────────────────

export async function loadCredentials() {
  try {
    return JSON.parse(await readFile(CREDENTIALS_FILE, "utf8"));
  } catch {
    return null;
  }
}

async function saveCredentials(creds) {
  await mkdir(CONFIG_DIR, { recursive: true });
  await writeFile(CREDENTIALS_FILE, JSON.stringify(creds, null, 2), { mode: 0o600 });
}

export async function clearCredentials() {
  try { await unlink(CREDENTIALS_FILE); } catch {}
}

export async function getAccessToken() {
  const creds = await loadCredentials();
  if (!creds?.access_token) {
    console.error("Not authenticated. Run: notion-cli auth login");
    process.exit(1);
  }

  // Check if token needs refresh
  if (creds.refresh_token && creds.expires_at && Date.now() > creds.expires_at - 60000) {
    try {
      const refreshed = await refreshToken(creds);
      return refreshed.access_token;
    } catch (err) {
      console.error(`Token refresh failed: ${err.message}. Run: notion-cli login`);
      process.exit(1);
    }
  }

  return creds.access_token;
}

// ─── Dynamic Client Registration ─────────────────────────

async function registerClient(redirectUri) {
  const res = await fetch(REGISTER_URL, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      client_name: "notion-cli-tool",
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

// ─── PKCE ─────────────────────────────────────────────────

function generatePKCE() {
  const verifier = randomBytes(32).toString("base64url");
  const challenge = createHash("sha256").update(verifier).digest("base64url");
  return { verifier, challenge };
}

// ─── Token Refresh ────────────────────────────────────────

async function refreshToken(creds) {
  const body = new URLSearchParams({
    grant_type: "refresh_token",
    refresh_token: creds.refresh_token,
    client_id: creds.client_id,
  });

  const res = await fetch(TOKEN_URL, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body,
  });

  if (!res.ok) {
    throw new Error(`Token refresh failed: ${res.status}`);
  }

  const data = await res.json();
  const updated = {
    ...creds,
    access_token: data.access_token,
    refresh_token: data.refresh_token || creds.refresh_token,
    expires_at: data.expires_in ? Date.now() + data.expires_in * 1000 : undefined,
    refreshed_at: new Date().toISOString(),
  };

  await saveCredentials(updated);
  return updated;
}

// ─── OAuth Login Flow ─────────────────────────────────────

export async function login(port = 9876) {
  const redirectUri = `http://localhost:${port}/callback`;

  // Step 1: Dynamic client registration
  console.log("Registering OAuth client with Notion MCP...");
  const client = await registerClient(redirectUri);
  const clientId = client.client_id;

  // Step 2: Generate PKCE challenge
  const { verifier, challenge } = generatePKCE();

  // Step 3: Generate state for CSRF protection
  const state = randomBytes(16).toString("hex");

  return new Promise((resolve, reject) => {
    const server = createServer(async (req, res) => {
      const url = new URL(req.url, `http://localhost:${port}`);

      if (url.pathname !== "/callback") {
        res.writeHead(404);
        res.end("Not found");
        return;
      }

      const error = url.searchParams.get("error");
      if (error) {
        const desc = url.searchParams.get("error_description") || error;
        res.writeHead(200, { "Content-Type": "text/html" });
        res.end(html("Authorization Failed", `<p>${desc}</p>`));
        server.close();
        reject(new Error(`OAuth error: ${desc}`));
        return;
      }

      const code = url.searchParams.get("code");
      const returnedState = url.searchParams.get("state");

      if (!code) {
        res.writeHead(400, { "Content-Type": "text/html" });
        res.end(html("Error", "<p>No authorization code received.</p>"));
        server.close();
        reject(new Error("No authorization code received"));
        return;
      }

      if (returnedState !== state) {
        res.writeHead(400, { "Content-Type": "text/html" });
        res.end(html("Error", "<p>State mismatch — possible CSRF attack.</p>"));
        server.close();
        reject(new Error("OAuth state mismatch"));
        return;
      }

      try {
        // Step 4: Exchange code for token
        const tokenBody = new URLSearchParams({
          grant_type: "authorization_code",
          code,
          redirect_uri: redirectUri,
          client_id: clientId,
          code_verifier: verifier,
        });

        const tokenRes = await fetch(TOKEN_URL, {
          method: "POST",
          headers: { "Content-Type": "application/x-www-form-urlencoded" },
          body: tokenBody,
        });

        if (!tokenRes.ok) {
          const errText = await tokenRes.text();
          throw new Error(`Token exchange failed (${tokenRes.status}): ${errText}`);
        }

        const tokenData = await tokenRes.json();

        // Save credentials
        const credentials = {
          access_token: tokenData.access_token,
          refresh_token: tokenData.refresh_token,
          token_type: tokenData.token_type,
          expires_at: tokenData.expires_in ? Date.now() + tokenData.expires_in * 1000 : undefined,
          client_id: clientId,
          obtained_at: new Date().toISOString(),
        };

        await saveCredentials(credentials);

        res.writeHead(200, { "Content-Type": "text/html" });
        res.end(html("✓ Authenticated with Notion",
          "<p>You can close this tab and return to the terminal.</p>"));

        server.close();
        resolve(credentials);
      } catch (err) {
        res.writeHead(500, { "Content-Type": "text/html" });
        res.end(html("Token Exchange Failed", `<p>${err.message}</p>`));
        server.close();
        reject(err);
      }
    });

    server.listen(port, async () => {
      const authUrl = new URL(AUTHORIZE_URL);
      authUrl.searchParams.set("client_id", clientId);
      authUrl.searchParams.set("response_type", "code");
      authUrl.searchParams.set("redirect_uri", redirectUri);
      authUrl.searchParams.set("state", state);
      authUrl.searchParams.set("code_challenge", challenge);
      authUrl.searchParams.set("code_challenge_method", "S256");

      console.log(`\nOpening browser for Notion authorization...`);
      console.log(`If browser doesn't open, visit:\n${authUrl.toString()}\n`);

      try {
        const open = (await import("open")).default;
        await open(authUrl.toString());
      } catch {}

      console.log("Waiting for authorization...");
    });

    setTimeout(() => {
      server.close();
      reject(new Error("Authorization timed out after 5 minutes"));
    }, 5 * 60 * 1000);
  });
}

function html(title, body) {
  return `<!DOCTYPE html><html><head><title>${title}</title>
<style>body{font-family:system-ui;max-width:480px;margin:80px auto;text-align:center;}</style>
</head><body><h1>${title}</h1>${body}</body></html>`;
}
