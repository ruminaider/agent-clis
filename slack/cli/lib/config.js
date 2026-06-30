import { createRequire } from "node:module";
import { homedir, platform } from "node:os";
import { join } from "node:path";

const require = createRequire(import.meta.url);
const { version: PACKAGE_JSON_VERSION } = require("../package.json");

export const TOOL_NAME = "slack";
export const PACKAGE_NAME = "@ruminaider/slack-cli";
export const CLI_NAME = "slack-cli";
export const PACKAGE_VERSION = PACKAGE_JSON_VERSION;

export const CONFIG_DIR = join(homedir(), ".config", CLI_NAME);
export const CREDENTIALS_FILE = join(CONFIG_DIR, "credentials.json");

// Environment overrides (CI / headless use).
export const CONFIG_ENV_KEYS = Object.freeze({
  token: "SLACK_TOKEN", // xoxc- web token
  cookie: "SLACK_COOKIE", // xoxd- d cookie value
  team: "SLACK_TEAM", // default team name, id, or host
});

// Slack desktop app data locations by platform.
function slackAppDir() {
  const home = homedir();
  switch (platform()) {
    case "darwin":
      return join(home, "Library", "Application Support", "Slack");
    case "linux":
      return join(home, ".config", "Slack");
    case "win32":
      return join(process.env.APPDATA || join(home, "AppData", "Roaming"), "Slack");
    default:
      return null;
  }
}

export const SLACK_APP_DIR = slackAppDir();
export const SLACK_LEVELDB_DIR = SLACK_APP_DIR
  ? join(SLACK_APP_DIR, "Local Storage", "leveldb")
  : null;

// Slack has shipped the Cookies file at the app root and, on some builds, under
// Network/ or Default/. Resolution tries each in order at extraction time.
export const SLACK_COOKIE_CANDIDATES = SLACK_APP_DIR
  ? [
      join(SLACK_APP_DIR, "Cookies"),
      join(SLACK_APP_DIR, "Network", "Cookies"),
      join(SLACK_APP_DIR, "Default", "Cookies"),
    ]
  : [];

// macOS Keychain accounts that have held the Slack cookie-encryption key.
export const MACOS_KEYCHAIN_SERVICES = ["Slack Safe Storage", "Slack Key", "Slack App Store Key"];

export const DEFAULT_API_HOST = "slack.com";
