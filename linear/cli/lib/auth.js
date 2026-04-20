import {
  CLI_NAME,
  CREDENTIALS_FILE,
  DEFAULT_AUTH_PORT,
  PACKAGE_NAME,
  PACKAGE_VERSION,
  TOOL_NAME,
} from "./config.js";

function scaffold(feature) {
  throw new Error(`${CLI_NAME} scaffold: ${feature} is not implemented yet`);
}

export async function loadCredentials() {
  return scaffold("loadCredentials");
}

export async function saveCredentials() {
  return scaffold("saveCredentials");
}

export async function clearCredentials() {
  return scaffold("clearCredentials");
}

export async function refreshToken() {
  return scaffold("refreshToken");
}

export async function login() {
  return scaffold("login");
}

export async function getAccessToken() {
  return scaffold("getAccessToken");
}

export const AUTH_CONTEXT = Object.freeze({
  cliName: CLI_NAME,
  packageName: PACKAGE_NAME,
  packageVersion: PACKAGE_VERSION,
  toolName: TOOL_NAME,
  credentialsFile: CREDENTIALS_FILE,
  defaultAuthPort: DEFAULT_AUTH_PORT,
});
