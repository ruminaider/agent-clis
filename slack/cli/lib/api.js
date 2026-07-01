// Slack web API client for native-session (xoxc + xoxd) credentials.
//
// xoxc tokens only authenticate when paired with the xoxd `d` cookie, so every
// request sends the token in the form body and the cookie in the Cookie header,
// against the workspace host. Command wrappers take resolved credentials so
// this module never imports auth.js (no import cycle).

import { DEFAULT_API_HOST } from "./config.js";

class SlackApiError extends Error {
  constructor(method, error, response) {
    super(`Slack API ${method} failed: ${error}`);
    this.name = "SlackApiError";
    this.slackError = error;
    this.response = response;
  }
}

function apiHost(creds) {
  return creds?.host || DEFAULT_API_HOST;
}

function buildBody(token, params) {
  const body = new URLSearchParams();
  body.set("token", token);
  for (const [key, value] of Object.entries(params || {})) {
    if (value === undefined || value === null || value === "") continue;
    body.set(key, typeof value === "object" ? JSON.stringify(value) : String(value));
  }
  return body;
}

// Low-level call. Returns the parsed Slack response on ok:true, throws
// SlackApiError otherwise.
export async function webApiCall(method, params, creds) {
  if (!creds?.token || !creds?.cookie) {
    throw new Error("Missing Slack credentials (token + cookie).");
  }
  const url = `https://${apiHost(creds)}/api/${method}`;
  // The xoxd `d` cookie value is stored and transmitted percent-encoded; send
  // it verbatim. Re-encoding it (e.g. encodeURIComponent) corrupts the trailing
  // `%3D` and Slack rejects the request with invalid_auth. The `d-s` companion
  // (Enterprise Grid / SSO) is appended when present.
  const cookieValue = creds.cookie.startsWith("xoxd-") ? creds.cookie : `xoxd-${creds.cookie}`;
  const cookieHeader = creds.cookieDs ? `d=${cookieValue}; d-s=${creds.cookieDs}` : `d=${cookieValue}`;

  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
      Cookie: cookieHeader,
      Accept: "application/json",
    },
    body: buildBody(creds.token, params),
  });

  if (!res.ok) {
    throw new Error(`Slack API ${method} HTTP ${res.status}: ${await res.text()}`);
  }
  const data = await res.json();
  if (!data.ok) {
    throw new SlackApiError(method, data.error || "unknown_error", data);
  }
  return data;
}

const num = (v) => (v === undefined || v === null || v === "" ? undefined : Number(v));

// ─── auth ────────────────────────────────────────────────────
export function authTest(creds) {
  return webApiCall("auth.test", {}, creds);
}

// ─── channels / conversations ────────────────────────────────
export function channelList(creds, options = {}) {
  return webApiCall("conversations.list", {
    types: options.types || "public_channel,private_channel,mpim,im",
    limit: num(options.limit) ?? 200,
    cursor: options.cursor,
    exclude_archived: options.includeArchived ? false : true,
  }, creds);
}

export function channelInfo(creds, channel) {
  return webApiCall("conversations.info", { channel }, creds);
}

export function channelHistory(creds, channel, options = {}) {
  return webApiCall("conversations.history", {
    channel,
    limit: num(options.limit) ?? 50,
    cursor: options.cursor,
    oldest: options.oldest,
    latest: options.latest,
    inclusive: options.inclusive ? true : undefined,
  }, creds);
}

export function channelMembers(creds, channel, options = {}) {
  return webApiCall("conversations.members", {
    channel,
    limit: num(options.limit) ?? 200,
    cursor: options.cursor,
  }, creds);
}

export function channelJoin(creds, channel) {
  return webApiCall("conversations.join", { channel }, creds);
}

export function channelCreate(creds, name, options = {}) {
  return webApiCall("conversations.create", {
    name,
    is_private: options.private ? true : undefined,
  }, creds);
}

// ─── threads ─────────────────────────────────────────────────
export function threadRead(creds, channel, ts, options = {}) {
  return webApiCall("conversations.replies", {
    channel,
    ts,
    limit: num(options.limit) ?? 100,
    cursor: options.cursor,
  }, creds);
}

// ─── messages ────────────────────────────────────────────────
export function messageSend(creds, channel, text, options = {}) {
  return webApiCall("chat.postMessage", {
    channel,
    text,
    thread_ts: options.threadTs,
    reply_broadcast: options.broadcast ? true : undefined,
    blocks: options.blocks,
  }, creds);
}

export function messageUpdate(creds, channel, ts, text, options = {}) {
  return webApiCall("chat.update", { channel, ts, text, blocks: options.blocks }, creds);
}

export function messageDelete(creds, channel, ts) {
  return webApiCall("chat.delete", { channel, ts }, creds);
}

// ─── search ──────────────────────────────────────────────────
export function searchMessages(creds, query, options = {}) {
  return webApiCall("search.messages", {
    query,
    count: num(options.limit) ?? 20,
    page: num(options.page),
    sort: options.sort,
    sort_dir: options.sortDir,
  }, creds);
}

export function searchFiles(creds, query, options = {}) {
  return webApiCall("search.files", {
    query,
    count: num(options.limit) ?? 20,
    page: num(options.page),
  }, creds);
}

export function searchAll(creds, query, options = {}) {
  return webApiCall("search.all", {
    query,
    count: num(options.limit) ?? 20,
    page: num(options.page),
  }, creds);
}

// ─── users ───────────────────────────────────────────────────
export function userList(creds, options = {}) {
  return webApiCall("users.list", {
    limit: num(options.limit) ?? 200,
    cursor: options.cursor,
  }, creds);
}

export function userInfo(creds, user) {
  return webApiCall("users.info", { user }, creds);
}

// ─── reactions ───────────────────────────────────────────────
export function reactionAdd(creds, channel, ts, name) {
  return webApiCall("reactions.add", { channel, timestamp: ts, name }, creds);
}

export function reactionRemove(creds, channel, ts, name) {
  return webApiCall("reactions.remove", { channel, timestamp: ts, name }, creds);
}

// ─── files ───────────────────────────────────────────────────
export function fileList(creds, options = {}) {
  return webApiCall("files.list", {
    channel: options.channel,
    user: options.user,
    count: num(options.limit) ?? 50,
    page: num(options.page),
  }, creds);
}

export function fileInfo(creds, file) {
  return webApiCall("files.info", { file }, creds);
}

// ─── pins ────────────────────────────────────────────────────
export function pinList(creds, channel) {
  return webApiCall("pins.list", { channel }, creds);
}

export function pinAdd(creds, channel, ts) {
  return webApiCall("pins.add", { channel, timestamp: ts }, creds);
}

export function pinRemove(creds, channel, ts) {
  return webApiCall("pins.remove", { channel, timestamp: ts }, creds);
}

// ─── canvases ────────────────────────────────────────────────
export function canvasList(creds, options = {}) {
  return webApiCall("files.list", {
    types: "canvas",
    count: num(options.limit) ?? 50,
    channel: options.channel,
  }, creds);
}

export function canvasGet(creds, canvas) {
  // Canvases are files; files.info returns the canvas file record, including the
  // url fields Slack provides for fetching the content.
  return webApiCall("files.info", { file: canvas }, creds);
}

export { SlackApiError };
