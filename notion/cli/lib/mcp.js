/**
 * MCP client for Notion's remote MCP server.
 * Handles sessions, tool calls, and JSON and SSE responses.
 */
import { getAccessToken } from "./auth.js";

const MCP_URL = "https://mcp.notion.com/mcp";
const PROTOCOL_VERSION = "2025-03-26";
const CALL_TIMEOUT_MS = 60000;
const MAX_RESPONSE_BYTES = 8 * 1024 * 1024;
const MAX_ERROR_BYTES = 64 * 1024;

let _sessionId = null;
let _initialized = false;
let _nextId = 1;

function nextId() {
  return _nextId++;
}

async function* bodyChunks(body, signal, limit, label = "MCP response") {
  if (!body) return;
  const reader = body.getReader();
  const cancel = () => { void reader.cancel().catch(() => {}); };
  signal.addEventListener("abort", cancel, { once: true });
  try {
    const decoder = new TextDecoder();
    let bytes = 0;
    while (true) {
      signal.throwIfAborted();
      const { value, done } = await reader.read();
      signal.throwIfAborted();
      if (done) break;
      bytes += value.byteLength;
      if (bytes > limit) throw new Error(`${label} exceeds ${limit} bytes`);
      const text = decoder.decode(value, { stream: true });
      if (text) yield text;
    }
    const tail = decoder.decode();
    if (tail) yield tail;
  } finally {
    signal.removeEventListener("abort", cancel);
    reader.releaseLock();
  }
}

async function readText(body, signal, limit, label) {
  let text = "";
  for await (const chunk of bodyChunks(body, signal, limit, label)) text += chunk;
  return text;
}

function matchesReply(data, id) {
  return data?.id === id && !Object.hasOwn(data, "method")
    && (Object.hasOwn(data, "result") || Object.hasOwn(data, "error"));
}

async function readSse(body, signal, id) {
  let partialLine = "";
  let skipLF = false;
  const dataLines = [];
  for await (const chunk of bodyChunks(body, signal, MAX_RESPONSE_BYTES)) {
    // A CR can end a line before its optional LF arrives in the next chunk.
    let start = skipLF && chunk.startsWith("\n") ? 1 : 0;
    for (const end of chunk.matchAll(/\r\n?|\n/g)) {
      if (end.index < start) continue;
      const line = partialLine + chunk.slice(start, end.index);
      partialLine = "";
      start = end.index + end[0].length;

      if (line === "") {
        const data = dataLines.join("\n");
        dataLines.length = 0;
        if (!data) continue;
        const reply = JSON.parse(data);
        if (matchesReply(reply, id)) return reply;
      } else if (line === "data" || line.startsWith("data:")) {
        dataLines.push(line.slice(5).replace(/^ /, ""));
      }
    }
    partialLine += chunk.slice(start);
    skipLF = chunk.endsWith("\r");
  }
  throw new Error(`No MCP response for request ${id}`);
}

/**
 * Send a JSON-RPC request to the MCP server.
 */
async function mcpRequest(method, signal, params = {}, isNotification = false) {
  signal.throwIfAborted();
  const token = await getAccessToken();
  signal.throwIfAborted();

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
    signal,
  });

  try {
    signal.throwIfAborted();
    const sid = res.headers.get("mcp-session-id");
    if (sid) _sessionId = sid;

    if (!res.ok) {
      const failure = `MCP request failed (${res.status})`;
      const text = await readText(res.body, signal, MAX_ERROR_BYTES, `${failure}: response body`);
      throw new Error(`${failure}: ${text}`);
    }

    if (isNotification) return null;

    const type = (res.headers.get("content-type") ?? "").split(";", 1)[0].trim().toLowerCase();
    let data;
    if (type === "application/json") {
      data = JSON.parse(await readText(res.body, signal, MAX_RESPONSE_BYTES));
      if (!matchesReply(data, body.id)) throw new Error(`No MCP response for request ${body.id}`);
    } else if (type === "text/event-stream") {
      data = await readSse(res.body, signal, body.id);
    } else {
      throw new Error(`Unsupported MCP response content type: ${type || "(missing)"}`);
    }

    if (data.error) {
      throw new Error(`MCP error ${data.error.code}: ${data.error.message}`);
    }
    return data.result;
  } finally {
    // Do not wait for the server to close the stream after a matching reply.
    void res.body?.cancel().catch(() => {});
  }
}

/**
 * Initialize the MCP session once per CLI invocation.
 */
async function ensureInitialized(signal) {
  if (_initialized) return;

  await mcpRequest("initialize", signal, {
    protocolVersion: PROTOCOL_VERSION,
    capabilities: {},
    clientInfo: { name: "notion-cli", version: "1.0.0" },
  });

  await mcpRequest("notifications/initialized", signal, {}, true);
  _initialized = true;
}

async function sessionRequest(method, params) {
  const controller = new AbortController();
  const { signal } = controller;
  let timer;
  const deadline = new Promise((_, reject) => {
    timer = setTimeout(() => {
      const error = new Error(
        `MCP call timed out after ${CALL_TIMEOUT_MS / 1000} seconds. The outcome is unknown. Check Notion before retrying a write.`,
      );
      reject(error);
      controller.abort(error);
    }, CALL_TIMEOUT_MS);
  });
  try {
    const request = ensureInitialized(signal).then(() => mcpRequest(method, signal, params));
    return await Promise.race([request, deadline]);
  } finally {
    clearTimeout(timer);
  }
}

/**
 * Call an MCP tool and return the result.
 */
export async function callTool(name, args = {}) {
  const result = await sessionRequest("tools/call", {
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
 * List available MCP tools.
 */
export async function listTools() {
  const result = await sessionRequest("tools/list");
  return result.tools;
}
