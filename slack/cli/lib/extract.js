// Native-session credential extraction from the local Slack desktop app.
//
// Two pieces are needed to call the Slack web API as the logged-in user:
//   - the xoxc web token(s), one per workspace, stored in the app's LevelDB
//     localStorage under the `localConfig_v2` key
//   - the xoxd `d` cookie, stored encrypted in the app's Chromium SQLite cookie
//     store and decrypted with a per-platform key
//
// The LevelDB data blocks are Snappy-compressed, so the surrounding JSON is not
// readable by a plain scan. The xoxc tokens, however, are unique random strings
// that Snappy always stores as verbatim literals, so we regex them straight out
// of the raw bytes and enrich each one over the network with auth.test rather
// than parsing the compressed structure. Everything uses Node built-ins only
// (node:sqlite, node:crypto, node:child_process, node:fs); no npm dependencies.

import { readFileSync, readdirSync, existsSync } from "node:fs";
import { join } from "node:path";
import { platform } from "node:os";
import { execFileSync } from "node:child_process";
import { pbkdf2Sync, createDecipheriv, createHash } from "node:crypto";
import { createRequire } from "node:module";
import {
  SLACK_LEVELDB_DIR,
  SLACK_COOKIE_CANDIDATES,
  MACOS_KEYCHAIN_SERVICES,
} from "./config.js";

// ─── xoxc tokens from LevelDB ───────────────────────────────

// xoxc tokens look like `xoxc-<digits>-<digits>-<digits>-<hex>`; the trailing
// segment is long, so a 40-char minimum avoids matching a truncated fragment
// left at a Snappy block boundary.
const TOKEN_RE = /xoxc-[A-Za-z0-9-]{40,}/g;

// Regex every xoxc token out of the raw LevelDB files and dedupe. Tokens are
// unique literals that survive Snappy compression intact; we do not try to
// parse the surrounding JSON. Any valid token wins after auth.test enrichment,
// so order here does not matter.
export function extractTokens() {
  if (!SLACK_LEVELDB_DIR || !existsSync(SLACK_LEVELDB_DIR)) {
    throw new Error(
      "Slack desktop app data not found. Open the Slack app and sign in, or import credentials manually with `slack-cli auth import`.",
    );
  }

  const files = readdirSync(SLACK_LEVELDB_DIR)
    .filter((name) => name.endsWith(".ldb") || name.endsWith(".log"))
    .map((name) => join(SLACK_LEVELDB_DIR, name));

  const tokens = new Set();
  for (const file of files) {
    let text;
    try {
      text = readFileSync(file).toString("latin1");
    } catch {
      continue;
    }
    for (const match of text.matchAll(TOKEN_RE)) tokens.add(match[0]);
  }

  const list = [...tokens];
  if (list.length === 0) {
    throw new Error(
      "No Slack workspace tokens found in the desktop app. Sign in to Slack, or import credentials manually with `slack-cli auth import`.",
    );
  }
  return list;
}

// ─── xoxd cookie from the Chromium SQLite store ─────────────

function resolveCookieDb() {
  for (const path of SLACK_COOKIE_CANDIDATES) {
    if (existsSync(path)) return path;
  }
  throw new Error("Slack cookie store not found. Sign in to the Slack desktop app first.");
}

// node:sqlite is a built-in but was flag-gated before Node 24, so it is loaded
// lazily here: only auto-extraction touches the cookie DB, leaving the env-var
// and `auth import` paths usable on older Node.
function loadSqlite() {
  const require = createRequire(import.meta.url);
  try {
    return require("node:sqlite");
  } catch (err) {
    throw new Error(
      `Reading the Slack cookie store needs Node's built-in node:sqlite (Node 24+, or Node 22–23 with --experimental-sqlite). Upgrade Node, or use \`slack-cli auth import\`. (${err.message})`,
    );
  }
}

// Read the encrypted `d` cookie (required) and its `d-s` companion (optional,
// present on Enterprise Grid and some SSO setups) from the cookie store.
function readEncryptedCookies() {
  const { DatabaseSync } = loadSqlite();
  const dbPath = resolveCookieDb();
  const db = new DatabaseSync(dbPath, { readOnly: true });
  try {
    const pick = (name) =>
      db
        .prepare(
          "SELECT encrypted_value, host_key FROM cookies WHERE name = ? ORDER BY length(encrypted_value) DESC LIMIT 1",
        )
        .get(name);
    const dRow = pick("d");
    if (!dRow?.encrypted_value) {
      throw new Error("No `d` cookie present in the Slack cookie store.");
    }
    // node:sqlite returns BLOBs as Uint8Array.
    const toEntry = (row) =>
      row?.encrypted_value
        ? { encrypted: Buffer.from(row.encrypted_value), hostKey: row.host_key || ".slack.com" }
        : null;
    return { d: toEntry(dRow), ds: toEntry(pick("d-s")) };
  } finally {
    db.close();
  }
}

// Chromium prepends a 32-byte SHA-256(host_key) to the plaintext for newer
// cookie store versions. Strip it when present so we return the raw value.
function stripHostPrefix(plaintext, hostKey) {
  if (plaintext.length <= 32) return plaintext;
  const expected = createHash("sha256").update(hostKey).digest();
  if (plaintext.subarray(0, 32).equals(expected)) {
    return plaintext.subarray(32);
  }
  return plaintext;
}

function macosDecryptionKey() {
  let lastError;
  for (const service of MACOS_KEYCHAIN_SERVICES) {
    try {
      const secret = execFileSync(
        "security",
        ["find-generic-password", "-w", "-s", service],
        { encoding: "utf8" },
      ).trim();
      if (secret) {
        return pbkdf2Sync(secret, "saltysalt", 1003, 16, "sha1");
      }
    } catch (err) {
      lastError = err;
    }
  }
  throw new Error(
    `Could not read the Slack cookie-encryption key from the macOS Keychain${
      lastError ? ` (${lastError.message})` : ""
    }. Approve the Keychain prompt, or import credentials manually with \`slack-cli auth import\`.`,
  );
}

function linuxDecryptionKey() {
  // Slack on Linux commonly uses Chromium's hardcoded v10 fallback key.
  return pbkdf2Sync("peanuts", "saltysalt", 1, 16, "sha1");
}

// requireXoxd validates the decrypted value looks like a `d` cookie (`xoxd-`).
// The `d-s` companion is an opaque session value, so it passes requireXoxd:false.
function decryptCookieValue(encrypted, hostKey, requireXoxd = true) {
  const prefix = encrypted.subarray(0, 3).toString("utf8");
  if (prefix !== "v10" && prefix !== "v11") {
    // Some stores keep the value in plaintext (older Slack builds).
    const asText = encrypted.toString("utf8");
    if (!requireXoxd || asText.startsWith("xoxd-")) return asText;
    throw new Error(`Unsupported cookie encryption version: ${JSON.stringify(prefix)}.`);
  }

  const os = platform();
  let key;
  if (os === "darwin") key = macosDecryptionKey();
  else if (os === "linux") key = linuxDecryptionKey();
  else throw new Error(`Cookie decryption is not implemented for platform ${os}. Use \`slack-cli auth import\`.`);

  const iv = Buffer.alloc(16, " ");
  const decipher = createDecipheriv("aes-128-cbc", key, iv);
  decipher.setAutoPadding(true);
  const body = encrypted.subarray(3);
  let plaintext;
  try {
    plaintext = Buffer.concat([decipher.update(body), decipher.final()]);
  } catch (err) {
    throw new Error(`Cookie decryption failed: ${err.message}. Try \`slack-cli auth import\`.`);
  }
  const value = stripHostPrefix(plaintext, hostKey).toString("utf8").replace(/\u0000+$/, "");
  if (requireXoxd && !value.startsWith("xoxd-")) {
    throw new Error("Decrypted cookie does not look like a Slack `d` cookie.");
  }
  return value;
}

// Returns { d, ds } where ds is null when the workspace has no d-s cookie.
export function extractCookies() {
  const { d, ds } = readEncryptedCookies();
  return {
    d: decryptCookieValue(d.encrypted, d.hostKey, true),
    ds: ds ? decryptCookieValue(ds.encrypted, ds.hostKey, false) : null,
  };
}

// ─── combined ────────────────────────────────────────────────

// Extract every workspace token plus the shared d cookie (and its optional d-s
// companion). The cookies are the same across all workspaces, so they are read
// once. Tokens are enriched into full workspace records by the caller via
// auth.test.
export function extractCredentials() {
  if (!SLACK_LEVELDB_DIR) {
    throw new Error(
      `Automatic extraction is not supported on platform ${platform()}. Use \`slack-cli auth import\`.`,
    );
  }
  const tokens = extractTokens();
  const { d, ds } = extractCookies();
  return { tokens, cookie: d, cookieDs: ds };
}
