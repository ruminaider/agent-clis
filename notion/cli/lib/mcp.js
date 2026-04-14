/**
 * MCP client for Notion's remote MCP server.
 * Handles session management, tool calls, and SSE response parsing.
 */
import { getAccessToken } from "./auth.js";

const MCP_URL = "https://mcp.notion.com/mcp";
const PROTOCOL_VERSION = "2025-03-26";

let _sessionId = null;
let _initialized = false;
let _nextId = 1;

function nextId() {
  return _nextId++;
}

/**
 * Send a JSON-RPC request to the MCP server
 */
async function mcpRequest(method, params = {}, isNotification = false) {
  const token = await getAccessToken();

  const body = {
    jsonrpc: "2.0",
    method,
    ...(Object.keys(params).length > 0 ? { params } : {}),
  };

  if (!isNotification) {
    body.id = nextId();
  }

  const headers = {
    "Content-Type": "application/json",
    Accept: "application/json, text/event-stream",
    Authorization: `Bearer ${token}`,
  };

  if (_sessionId) {
    headers["mcp-session-id"] = _sessionId;
  }

  const res = await fetch(MCP_URL, {
    method: "POST",
    headers,
    body: JSON.stringify(body),
  });

  // Capture session ID from response
  const sid = res.headers.get("mcp-session-id");
  if (sid) _sessionId = sid;

  if (!res.ok) {
    const errText = await res.text();
    throw new Error(`MCP request failed (${res.status}): ${errText}`);
  }

  if (isNotification) return null;

  // Parse SSE response
  const text = await res.text();
  const dataLines = text.split("\n").filter((l) => l.startsWith("data: "));

  if (dataLines.length === 0) {
    throw new Error("No data in MCP response");
  }

  const data = JSON.parse(dataLines[dataLines.length - 1].slice(6));

  if (data.error) {
    throw new Error(`MCP error ${data.error.code}: ${data.error.message}`);
  }

  return data.result;
}

/**
 * Initialize the MCP session (called once per CLI invocation)
 */
async function ensureInitialized() {
  if (_initialized) return;

  await mcpRequest("initialize", {
    protocolVersion: PROTOCOL_VERSION,
    capabilities: {},
    clientInfo: { name: "notion-cli", version: "1.0.0" },
  });

  await mcpRequest("notifications/initialized", {}, true);
  _initialized = true;
}

/**
 * Call an MCP tool and return the result
 */
export async function callTool(name, args = {}) {
  await ensureInitialized();

  const result = await mcpRequest("tools/call", {
    name,
    arguments: args,
  });

  if (result.isError) {
    const errText = result.content?.[0]?.text || "Unknown MCP tool error";
    throw new Error(errText);
  }

  // Extract text content
  const textParts = (result.content || [])
    .filter((c) => c.type === "text")
    .map((c) => c.text);

  const combined = textParts.join("\n");

  // Try to parse as JSON
  try {
    return JSON.parse(combined);
  } catch {
    return combined;
  }
}

/**
 * List available MCP tools
 */
export async function listTools() {
  await ensureInitialized();
  const result = await mcpRequest("tools/list");
  return result.tools;
}
