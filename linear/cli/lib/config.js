import { createRequire } from "node:module";
import { homedir } from "node:os";
import { readFile } from "node:fs/promises";
import { join } from "node:path";

const require = createRequire(import.meta.url);
const { version: PACKAGE_JSON_VERSION } = require("../package.json");

export const TOOL_NAME = "linear";
export const PACKAGE_NAME = "@ruminaider/linear-cli";
export const CLI_NAME = "linear-cli";
export const PACKAGE_VERSION = PACKAGE_JSON_VERSION;

export const DEFAULT_AUTH_PORT = 9886;
export const CONFIG_DIR = join(homedir(), ".config", CLI_NAME);
export const CREDENTIALS_FILE = join(CONFIG_DIR, "credentials.json");
export const CONFIG_FILE = join(CONFIG_DIR, "config.json");
export const MCP_BASE_URL = "https://mcp.linear.app";
export const MCP_URL = `${MCP_BASE_URL}/mcp`;
export const OAUTH_PROTECTED_RESOURCE_URL = `${MCP_BASE_URL}/.well-known/oauth-protected-resource`;
export const OAUTH_AUTHORIZATION_SERVER_URL = `${MCP_BASE_URL}/.well-known/oauth-authorization-server`;
export const OAUTH_AUTHORIZE_URL = `${MCP_BASE_URL}/authorize`;
export const OAUTH_TOKEN_URL = `${MCP_BASE_URL}/token`;
export const OAUTH_REGISTER_URL = `${MCP_BASE_URL}/register`;
export const OAUTH_REVOCATION_URL = `${MCP_BASE_URL}/token`;

export const CONFIG_ENV_KEYS = Object.freeze({
  apiKey: "LINEAR_API_KEY",
  defaultTeam: "LINEAR_DEFAULT_TEAM",
});

function normalizeConfigValue(value) {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

async function loadPersistedConfig() {
  try {
    const parsed = JSON.parse(await readFile(CONFIG_FILE, "utf8"));
    return { defaultTeam: normalizeConfigValue(parsed?.defaultTeam) };
  } catch {
    return { defaultTeam: null };
  }
}

export async function resolveConfigDefaults(options = {}) {
  const persisted = await loadPersistedConfig();
  const envTeam = normalizeConfigValue(process.env[CONFIG_ENV_KEYS.defaultTeam]);
  return {
    defaultTeam: normalizeConfigValue(options.defaultTeam) ?? envTeam ?? persisted.defaultTeam,
  };
}
