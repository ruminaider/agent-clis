import { createRequire } from "node:module";
import { homedir } from "node:os";
import { join } from "node:path";

const require = createRequire(import.meta.url);
const { version: PACKAGE_JSON_VERSION } = require("../package.json");

export const TOOL_NAME = "metabase";
export const PACKAGE_NAME = "@ruminaider/metabase-cli";
export const CLI_NAME = "metabase-cli";
export const PACKAGE_VERSION = PACKAGE_JSON_VERSION;

export const CONFIG_DIR = join(homedir(), ".config", CLI_NAME);
export const CREDENTIALS_FILE = join(CONFIG_DIR, "credentials.json");

// Environment overrides. Names match the metabase-mcp-server so the CLI is a
// drop-in swap for the MCP with the same environment.
export const ENV = Object.freeze({
  url: "METABASE_URL",
  apiKey: "METABASE_API_KEY",
  sessionToken: "METABASE_SESSION_TOKEN",
  email: "METABASE_USER_EMAIL",
  password: "METABASE_PASSWORD",
  readOnly: "METABASE_READ_ONLY",
});

export const MAX_BATCH_CONCURRENCY = 5;
export const RETRY_ATTEMPTS = 3;
