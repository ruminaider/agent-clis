import { homedir } from "node:os";
import { join } from "node:path";

export const TOOL_NAME = "linear";
export const PACKAGE_NAME = "@ruminaider/linear-cli";
export const CLI_NAME = "linear-cli";
export const PACKAGE_VERSION = "0.0.0";

export const COMMAND_NAMESPACES = Object.freeze([
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
export const SKILL_PATH = join("skill", TOOL_NAME, "SKILL.md");

export const CONFIG_ENV_KEYS = Object.freeze({
  apiKey: "LINEAR_API_KEY",
  defaultTeam: "LINEAR_DEFAULT_TEAM",
  defaultWorkspace: "LINEAR_DEFAULT_WORKSPACE",
});

export const CONFIG_PRECEDENCE = Object.freeze([
  "explicit CLI flag",
  `env (${CONFIG_ENV_KEYS.defaultTeam}, ${CONFIG_ENV_KEYS.defaultWorkspace}, ${CONFIG_ENV_KEYS.apiKey})`,
  `persisted config (${CONFIG_FILE})`,
]);

export const CONFIG_DEFAULTS = Object.freeze({
  defaultTeam: null,
  defaultWorkspace: null,
});
