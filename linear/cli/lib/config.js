import { homedir } from "node:os";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

export const TOOL_NAME = "linear";
export const PACKAGE_NAME = "@ruminaider/linear-cli";
export const CLI_NAME = "linear-cli";
export const PACKAGE_VERSION = "0.1.0";

export const RESERVED_COMMAND_NAMESPACES = Object.freeze([
  "auth",
  "search",
  "issue",
  "project",
  "team",
  "label",
  "comment",
  "config",
  "mcp",
]);

export const MVP_COMMAND_NAMESPACES = Object.freeze([
  "auth",
  "project",
  "comment",
  "mcp",
]);

export const DEFAULT_AUTH_PORT = 9886;
export const CONFIG_DIR = join(homedir(), ".config", CLI_NAME);
export const CREDENTIALS_FILE = join(CONFIG_DIR, "credentials.json");
export const CONFIG_FILE = join(CONFIG_DIR, "config.json");
export const CACHE_DIR = join(CONFIG_DIR, "cache");
export const MCP_BASE_URL = "https://mcp.linear.app";
export const MCP_URL = `${MCP_BASE_URL}/mcp`;
export const OAUTH_PROTECTED_RESOURCE_URL = `${MCP_BASE_URL}/.well-known/oauth-protected-resource`;
export const OAUTH_AUTHORIZATION_SERVER_URL = `${MCP_BASE_URL}/.well-known/oauth-authorization-server`;
export const OAUTH_AUTHORIZE_URL = `${MCP_BASE_URL}/authorize`;
export const OAUTH_TOKEN_URL = `${MCP_BASE_URL}/token`;
export const OAUTH_REGISTER_URL = `${MCP_BASE_URL}/register`;
export const OAUTH_REVOCATION_URL = `${MCP_BASE_URL}/token`;
export const SKILL_PATH = join("skill", TOOL_NAME, "SKILL.md");

export const CONFIG_ENV_KEYS = Object.freeze({
  apiKey: "LINEAR_API_KEY",
  defaultTeam: "LINEAR_DEFAULT_TEAM",
});

export const CONFIG_PRECEDENCE = Object.freeze([
  "explicit CLI flag",
  `env (${CONFIG_ENV_KEYS.defaultTeam}, ${CONFIG_ENV_KEYS.apiKey})`,
  `persisted config (${CONFIG_FILE})`,
]);

export const CONFIG_DEFAULTS = Object.freeze({
  defaultTeam: null,
});

const PERSISTED_CONFIG_KEYS = Object.freeze(["defaultTeam"]);

function normalizeConfigValue(value) {
  return typeof value === "string" && value.trim() ? value.trim() : null;
}

export function normalizePersistedConfig(config = {}) {
  const normalized = {};
  for (const key of PERSISTED_CONFIG_KEYS) {
    const value = normalizeConfigValue(config?.[key]);
    if (value !== null) {
      normalized[key] = value;
    }
  }
  return normalized;
}

export async function loadPersistedConfig() {
  try {
    const parsed = JSON.parse(await readFile(CONFIG_FILE, "utf8"));
    return normalizePersistedConfig(parsed);
  } catch {
    return {};
  }
}

export async function savePersistedConfig(config = {}) {
  await mkdir(CONFIG_DIR, { recursive: true });
  const normalized = normalizePersistedConfig(config);
  await writeFile(CONFIG_FILE, `${JSON.stringify(normalized, null, 2)}\n`, { mode: 0o600 });
  return normalized;
}

export async function resolveConfigDefaults(options = {}) {
  const persisted = await loadPersistedConfig();
  const env = {
    defaultTeam: normalizeConfigValue(process.env[CONFIG_ENV_KEYS.defaultTeam]),
  };

  return {
    defaultTeam: normalizeConfigValue(options.defaultTeam) ?? env.defaultTeam ?? persisted.defaultTeam ?? CONFIG_DEFAULTS.defaultTeam,
  };
}
