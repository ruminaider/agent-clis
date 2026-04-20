import { CLI_NAME, MCP_URL, PACKAGE_VERSION } from "./config.js";
import { getAccessToken } from "./auth.js";

const MCP_JSONRPC_VERSION = "2.0";
const DEFAULT_PROTOCOL_VERSIONS = ["2024-11-05", "2024-10-07", "2024-09-05", "2024-08-06"];
const MCP_REQUEST_TIMEOUT_MS = 30000;

let sessionState = null;
let sessionInitPromise = null;
let nextRequestId = 1;

function createMcpError(code, message, details = {}) {
  const error = new Error(message);
  error.code = code;
  error.details = details;
  return error;
}

function ensureObject(value, context) {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    throw createMcpError("MCP_INVALID_RESPONSE", `Expected ${context} to be an object`, { value });
  }
  return value;
}

function getHeader(headers, name) {
  if (!headers) return null;
  if (typeof headers.get === "function") {
    return headers.get(name);
  }
  const lowered = name.toLowerCase();
  for (const [key, value] of Object.entries(headers)) {
    if (key.toLowerCase() === lowered) {
      return Array.isArray(value) ? value.join(", ") : value;
    }
  }
  return null;
}

function parseContentType(value) {
  return String(value ?? "").split(";")[0].trim().toLowerCase();
}

function parseSseEvents(text) {
  const events = [];
  const blocks = text.replace(/\r\n/g, "\n").split(/\n\n+/);
  for (const block of blocks) {
    const lines = block.split("\n");
    let event = "message";
    const dataLines = [];
    for (const line of lines) {
      if (!line || line.startsWith(":")) continue;
      const colonIndex = line.indexOf(":");
      const field = colonIndex === -1 ? line : line.slice(0, colonIndex);
      const rawValue = colonIndex === -1 ? "" : line.slice(colonIndex + 1).replace(/^ /, "");
      if (field === "event") event = rawValue;
      if (field === "data") dataLines.push(rawValue);
    }
    if (dataLines.length > 0) {
      events.push({ event, data: dataLines.join("\n") });
    }
  }
  return events;
}

async function readResponseBody(response) {
  const contentType = parseContentType(getHeader(response.headers, "content-type"));
  const text = await response.text();
  if (!text) return { contentType, body: null };

  if (contentType === "application/json" || contentType === "application/mcp+json") {
    try {
      return { contentType, body: JSON.parse(text) };
    } catch (cause) {
      throw createMcpError("MCP_INVALID_JSON", "Failed to parse JSON response from MCP server", { text, cause: String(cause) });
    }
  }

  if (contentType === "text/event-stream") {
    const events = parseSseEvents(text).map((entry) => {
      try {
        return { ...entry, data: JSON.parse(entry.data) };
      } catch {
        return entry;
      }
    });
    return { contentType, body: events };
  }

  return { contentType, body: text };
}

function normalizeProtocolVersions(serverCapabilities, serverVersion) {
  const seen = new Set();
  const versions = [];
  const candidates = [serverVersion, ...(Array.isArray(serverCapabilities?.protocolVersions) ? serverCapabilities.protocolVersions : [])];
  for (const candidate of candidates) {
    if (typeof candidate === "string" && candidate && !seen.has(candidate)) {
      seen.add(candidate);
      versions.push(candidate);
    }
  }
  return versions;
}

function selectProtocolVersion(result) {
  const serverSupported = normalizeProtocolVersions(result?.capabilities, result?.protocolVersion);
  const negotiated = serverSupported.find((version) => DEFAULT_PROTOCOL_VERSIONS.includes(version));
  return negotiated ?? DEFAULT_PROTOCOL_VERSIONS[0];
}

async function fetchAccessToken() {
  const token = await getAccessToken();
  if (typeof token !== "string" || !token.trim()) {
    throw createMcpError("MCP_AUTH_UNAVAILABLE", `${CLI_NAME} MCP transport requires an access token`, { tokenType: typeof token });
  }
  return token.trim();
}

async function performRequest({ method = "POST", body, sessionId = null, protocolVersion = null }) {
  const accessToken = await fetchAccessToken();
  const controller = new AbortController();
  const timeout = setTimeout(() => controller.abort(new Error("Request timed out")), MCP_REQUEST_TIMEOUT_MS);

  try {
    const headers = {
      Authorization: `Bearer ${accessToken}`,
      Accept: "application/json, text/event-stream",
      "Content-Type": "application/json",
      "User-Agent": `${CLI_NAME}/${PACKAGE_VERSION}`,
    };
    if (sessionId) headers["Mcp-Session-Id"] = sessionId;
    if (protocolVersion) headers["Mcp-Protocol-Version"] = protocolVersion;

    const response = await fetch(MCP_URL, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal: controller.signal,
    });

    const { contentType, body: parsedBody } = await readResponseBody(response);
    if (!response.ok) {
      throw createMcpError("MCP_HTTP_ERROR", `MCP request failed with HTTP ${response.status}`, {
        status: response.status,
        statusText: response.statusText,
        contentType,
        body: parsedBody,
      });
    }
    return { response, contentType, body: parsedBody };
  } catch (cause) {
    if (cause?.name === "AbortError") {
      throw createMcpError("MCP_TIMEOUT", "MCP request timed out", { timeout: MCP_REQUEST_TIMEOUT_MS });
    }
    if (cause?.code?.startsWith?.("MCP_")) throw cause;
    throw createMcpError("MCP_NETWORK_ERROR", "Failed to communicate with MCP server", { cause: String(cause) });
  } finally {
    clearTimeout(timeout);
  }
}

function extractRpcError(payload) {
  if (!payload || typeof payload !== "object") return null;
  if (payload.error && typeof payload.error === "object") return payload.error;
  if (Array.isArray(payload) && payload.length > 0 && payload[0]?.error) return payload[0].error;
  return null;
}

function extractRpcResult(payload) {
  if (!payload || typeof payload !== "object") return payload;
  if (Object.prototype.hasOwnProperty.call(payload, "result")) return payload.result;
  return payload;
}

function extractTerminalSsePayload(events, requestId) {
  if (!Array.isArray(events)) return events;

  const terminalEvent = events.find((event) => {
    const payload = event?.data;
    if (!payload || typeof payload !== "object") return false;
    if (payload.jsonrpc !== MCP_JSONRPC_VERSION) return false;
    if (!Object.prototype.hasOwnProperty.call(payload, "id")) return false;
    return payload.id === requestId;
  });

  return terminalEvent?.data ?? null;
}

function createRequestId() {
  const id = `req-${nextRequestId}`;
  nextRequestId += 1;
  return id;
}

async function rpc(method, params = {}, { sessionId = null, protocolVersion = null } = {}) {
  const request = { jsonrpc: MCP_JSONRPC_VERSION, id: createRequestId(), method, params };
  const { response, body, contentType } = await performRequest({ body: request, sessionId, protocolVersion });
  const nextSessionId = getHeader(response.headers, "mcp-session-id") ?? sessionId;

  if (contentType === "text/event-stream") {
    const payload = extractTerminalSsePayload(body, request.id);
    if (!payload) {
      throw createMcpError("MCP_INVALID_RESPONSE", "MCP SSE response did not include a terminal JSON-RPC payload", {
        method,
        params,
        requestId: request.id,
        body,
      });
    }
    const rpcError = extractRpcError(payload);
    if (rpcError) {
      throw createMcpError("MCP_RPC_ERROR", rpcError.message ?? "MCP RPC error", { error: rpcError, method, params });
    }
    return { result: extractRpcResult(payload), sessionId: nextSessionId };
  }

  const payload = ensureObject(body, "MCP JSON response");
  const rpcError = extractRpcError(payload);
  if (rpcError) {
    throw createMcpError("MCP_RPC_ERROR", rpcError.message ?? "MCP RPC error", { error: rpcError, method, params });
  }
  return { result: extractRpcResult(payload), sessionId: nextSessionId };
}

async function initializeWithFallback() {
  const attempts = [];

  for (const candidate of DEFAULT_PROTOCOL_VERSIONS) {
    try {
      const initializeResponse = await rpc(
        "initialize",
        {
          clientInfo: { name: CLI_NAME, version: PACKAGE_VERSION },
          capabilities: {},
          protocolVersion: candidate,
        },
        {},
      );

      const result = ensureObject(initializeResponse.result, "initialize result");
      return {
        initializeResponse,
        protocolVersion: selectProtocolVersion(result),
      };
    } catch (error) {
      attempts.push({ protocolVersion: candidate, code: error?.code, message: error?.message, details: error?.details });
    }
  }

  const lastAttempt = attempts[attempts.length - 1] ?? null;
  throw createMcpError(
    "MCP_PROTOCOL_NEGOTIATION_FAILED",
    "Unable to initialize MCP session with any supported protocol version",
    { attempts, lastAttempt },
  );
}

async function ensureSession() {
  if (sessionState?.initialized) return sessionState;
  if (!sessionInitPromise) {
    sessionInitPromise = (async () => {
      const { initializeResponse, protocolVersion } = await initializeWithFallback();
      const result = ensureObject(initializeResponse.result, "initialize result");
      const sessionId = initializeResponse.sessionId;

      sessionState = Object.freeze({
        initialized: true,
        protocolVersion,
        sessionId,
        serverInfo: result.serverInfo ?? null,
        capabilities: result.capabilities ?? null,
      });

      return sessionState;
    })().finally(() => {
      sessionInitPromise = null;
    });
  }

  return sessionInitPromise;
}

function unwrapToolList(result) {
  if (Array.isArray(result)) return result;
  if (Array.isArray(result?.tools)) return result.tools;
  if (Array.isArray(result?.items)) return result.items;
  throw createMcpError("MCP_INVALID_RESPONSE", "MCP tools/list response did not contain a tool array", { result });
}

function unwrapToolCall(result) {
  if (result && typeof result === "object") return result;
  throw createMcpError("MCP_INVALID_RESPONSE", "MCP tools/call response was malformed", { result });
}

export async function initializeMcpSession() {
  return ensureSession();
}

export async function resetMcpSession() {
  sessionState = null;
  sessionInitPromise = null;
}

export async function callTool(name, arguments_ = {}) {
  const session = await ensureSession();
  const response = await rpc(
    "tools/call",
    { name, arguments: arguments_ },
    { sessionId: session.sessionId, protocolVersion: session.protocolVersion },
  );
  return unwrapToolCall(response.result);
}

export async function listTools() {
  const session = await ensureSession();
  const response = await rpc(
    "tools/list",
    {},
    { sessionId: session.sessionId, protocolVersion: session.protocolVersion },
  );
  return unwrapToolList(response.result);
}

export const MCP_CONTEXT = Object.freeze({
  cliName: CLI_NAME,
  packageVersion: PACKAGE_VERSION,
  mcpUrl: MCP_URL,
  getAccessToken,
});
