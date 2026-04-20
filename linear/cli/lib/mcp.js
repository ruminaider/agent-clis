import { CLI_NAME, MCP_URL, PACKAGE_VERSION } from "./config.js";
import { getAccessToken } from "./auth.js";

function scaffold(feature) {
  throw new Error(`${CLI_NAME} scaffold: ${feature} is not implemented yet`);
}

export async function initializeMcpSession() {
  return scaffold("initializeMcpSession");
}

export async function resetMcpSession() {
  return scaffold("resetMcpSession");
}

export async function callTool() {
  return scaffold("callTool");
}

export async function listTools() {
  return scaffold("listTools");
}

export const MCP_CONTEXT = Object.freeze({
  cliName: CLI_NAME,
  packageVersion: PACKAGE_VERSION,
  mcpUrl: MCP_URL,
  getAccessToken,
});
