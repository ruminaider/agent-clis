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

// The xoxd `d` cookie value is stored and transmitted percent-encoded; send it
// verbatim. Re-encoding it (e.g. encodeURIComponent) corrupts the trailing
// `%3D` and Slack rejects the request with invalid_auth. The `d-s` companion
// (Enterprise Grid / SSO) is appended when present.
export function buildCookieHeader(creds) {
  const value = creds.cookie.startsWith("xoxd-") ? creds.cookie : `xoxd-${creds.cookie}`;
  return creds.cookieDs ? `d=${value}; d-s=${creds.cookieDs}` : `d=${value}`;
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

// Slack refuses workspace-scoped methods on an Enterprise Grid org-level token.
// These are the errors that mean "ask a workspace instead", not "you cannot do
// this at all", so a retry against a workspace token is the right answer.
const ORG_TOKEN_ERRORS = new Set(["enterprise_is_restricted", "missing_team_id", "team_is_restricted"]);

// Methods that address their target by name instead of by id. Retrying one on
// another workspace would act somewhere the caller never named — a channel
// created in whichever workspace happened to answer — so they demand --team.
const NAME_ADDRESSED_METHODS = new Set(["conversations.create"]);

const RATE_LIMIT_RETRIES = 2;

// Slack asks for 60s often enough that a lower ceiling guarantees a rejected
// retry, so honour what it asks for up to a couple of minutes.
const MAX_RETRY_AFTER_SECONDS = 120;

// Pages to read per workspace when sweeping a Grid org, so a huge org cannot
// turn one command into an unbounded crawl.
const MAX_SWEEP_PAGES = 20;

const RETRY_HINT = "Run `slack-cli auth login` to store a token for each workspace in your org, or target one with `--team <name|id|host>`.";

function teamLabel(creds) {
  return creds?.team?.name || creds?.team?.id || creds?.host || "workspace";
}

function orgTokenGuidance(method, error) {
  if (NAME_ADDRESSED_METHODS.has(method)) {
    return new Error(
      `Slack API ${method} failed: ${error}. Enterprise Grid refuses this on an org-level token, and it names its target rather than addressing an id, so retrying elsewhere would act on a workspace you did not choose. Re-run with \`--team <name|id|host>\`.`,
    );
  }
  const detail =
    error === "enterprise_is_restricted"
      ? "Enterprise Grid blocks workspace-scoped methods on an org-level token, and no workspace token was available to retry with."
      : "Slack scopes this method to a single workspace.";
  return new Error(`Slack API ${method} failed: ${error}. ${detail} ${RETRY_HINT}`);
}

// Low-level call. Returns the parsed Slack response on ok:true, throws
// SlackApiError otherwise.
async function rawCall(method, params, creds, attempt = 0) {
  if (!creds?.token || !creds?.cookie) {
    throw new Error("Missing Slack credentials (token + cookie).");
  }
  const url = `https://${apiHost(creds)}/api/${method}`;

  const res = await fetch(url, {
    method: "POST",
    headers: {
      "Content-Type": "application/x-www-form-urlencoded; charset=utf-8",
      Cookie: buildCookieHeader(creds),
      Accept: "application/json",
    },
    body: buildBody(creds.token, params),
  });

  // Slack throttles per method; a sweep across workspaces is the likeliest way
  // to hit it. Wait out Slack's own Retry-After rather than failing the command,
  // and say so on stderr so a long pause does not look like a hung CLI.
  if (res.status === 429) {
    const wait = Math.min(Number(res.headers.get("retry-after")) || 1, MAX_RETRY_AFTER_SECONDS);
    if (attempt < RATE_LIMIT_RETRIES) {
      process.stderr.write(
        `Slack rate-limited ${method}; waiting ${wait}s before retry ${attempt + 1} of ${RATE_LIMIT_RETRIES}.\n`,
      );
      await new Promise((resolve) => setTimeout(resolve, wait * 1000));
      return rawCall(method, params, creds, attempt + 1);
    }
    throw new Error(
      `Slack rate-limited ${method} and ${RATE_LIMIT_RETRIES} retries did not clear it (Slack asked for ${wait}s). Wait and re-run, or narrow the request with \`--team <name|id|host>\` so it touches one workspace instead of sweeping the org.`,
    );
  }
  if (!res.ok) {
    throw new Error(`Slack API ${method} HTTP ${res.status}: ${await res.text()}`);
  }
  const data = await res.json();
  if (!data.ok) {
    throw new SlackApiError(method, data.error || "unknown_error", data);
  }
  return data;
}

// Call Slack, and when the credentials are an Enterprise Grid org-level token
// that Slack refuses for this method, transparently retry against the user's
// workspace tokens. Regular (non-Grid) workspaces carry no fallbacks, so they
// take the direct path unchanged.
export async function webApiCall(method, params, creds) {
  try {
    return await rawCall(method, params, creds);
  } catch (err) {
    if (!(err instanceof SlackApiError) || !ORG_TOKEN_ERRORS.has(err.slackError)) throw err;
    const fallbacks = creds?.fallbacks || [];
    if (fallbacks.length === 0 || NAME_ADDRESSED_METHODS.has(method)) {
      throw orgTokenGuidance(method, err.slackError);
    }
    const attempts = [];
    for (const fallback of fallbacks) {
      try {
        return await rawCall(method, params, fallback);
      } catch (retryErr) {
        attempts.push(`${teamLabel(fallback)}: ${retryErr.slackError || retryErr.message}`);
        // Anything other than "ask a different workspace" is Slack's real answer
        // (a dead token, a policy refusal, a bad argument). Surface it instead of
        // burying it under whichever workspace happens to answer last.
        if (retryErr instanceof SlackApiError && !ORG_TOKEN_ERRORS.has(retryErr.slackError)) throw retryErr;
      }
    }
    throw new Error(
      `Slack API ${method} failed on the org-level token and on every workspace (${attempts.join("; ")}). ${RETRY_HINT}`,
    );
  }
}

const num = (v) => (v === undefined || v === null || v === "" ? undefined : Number(v));

// ─── auth ────────────────────────────────────────────────────
export function authTest(creds) {
  return webApiCall("auth.test", {}, creds);
}

// ─── channels / conversations ────────────────────────────────

// Which credentials should answer a channel listing. On Enterprise Grid a
// workspace token sees only its own workspace, so the org-level default asks
// every workspace and merges the results; `--all-teams` forces that same sweep
// for anyone signed in to several workspaces.
function listTargets(creds, options) {
  if (options.allTeams) return creds.teams?.length ? creds.teams : [creds];
  if (creds.orgLevel && creds.fallbacks?.length) return creds.fallbacks;
  return [creds];
}

export async function channelList(creds, options = {}) {
  const targets = listTargets(creds, options);
  const exclude_archived = options.includeArchived ? false : true;
  const types = options.types || "public_channel,private_channel,mpim,im";

  // One workspace keeps Slack's own contract: `--limit` is the page size and
  // `--cursor` pages through it.
  if (targets.length === 1) {
    return webApiCall(
      "conversations.list",
      { types, limit: num(options.limit) ?? 200, cursor: options.cursor, exclude_archived },
      targets[0],
    );
  }
  if (options.cursor) {
    throw new Error(
      "Paging with --cursor needs a single workspace. Re-run with `--team <name|id|host>`.",
    );
  }

  // Across workspaces `--limit` caps the merged result, so the flag still means
  // "how many channels do I want" and the sweep cannot balloon into hundreds of
  // calls. Without it, each workspace is paged to exhaustion under a page cap.
  const cap = num(options.limit);
  const params = { types, limit: Math.min(cap ?? 200, 200), exclude_archived };

  // Grid channels are often shared across workspaces, so dedupe by id and keep
  // the first sighting. A workspace that errors is reported rather than fatal;
  // only a clean sweep of failures fails the command.
  const byId = new Map();
  const teams = [];
  const failures = [];
  let reachedCap = false;
  for (const target of targets) {
    const label = { team_id: target.team?.id || null, team: target.team?.name || null };
    if (reachedCap) {
      teams.push({ ...label, skipped: "--limit reached before this workspace was read" });
      continue;
    }
    try {
      let cursor;
      let count = 0;
      let pages = 0;
      do {
        const res = await webApiCall("conversations.list", { ...params, cursor }, target);
        for (const channel of res.channels || []) {
          count += 1;
          if (!byId.has(channel.id)) byId.set(channel.id, channel);
        }
        cursor = res.response_metadata?.next_cursor || "";
        pages += 1;
      } while (cursor && pages < MAX_SWEEP_PAGES && (cap === undefined || byId.size < cap));
      reachedCap = cap !== undefined && byId.size >= cap;
      teams.push({
        ...label,
        count,
        // Carry the cursor so a cut-short workspace can be resumed instead of
        // re-read from the top.
        ...(cursor
          ? {
              truncated: reachedCap
                ? "stopped at --limit"
                : `stopped after ${MAX_SWEEP_PAGES} pages`,
              next_cursor: cursor,
              resume: `slack-cli channel list --team ${target.team?.id || target.team?.name} --cursor ${cursor}`,
            }
          : {}),
      });
    } catch (err) {
      teams.push({ ...label, error: err.slackError || err.message });
      failures.push(err);
    }
  }

  if (failures.length === targets.length) {
    throw new Error(
      `conversations.list failed on all ${targets.length} workspaces: ${teams
        .map((t) => `${t.team || t.team_id || "unknown"}: ${t.error}`)
        .join("; ")}`,
    );
  }

  // A caller that checks `ok` and reads `channels` must not mistake a partial
  // sweep for the whole org, so incompleteness is stated at the top level.
  const incomplete = teams.filter((t) => t.error || t.truncated || t.skipped);
  return {
    ok: true,
    channels: [...byId.values()],
    teams,
    ...(incomplete.length
      ? {
          partial: true,
          partial_reason: `This list is incomplete: ${incomplete.length} of ${teams.length} workspaces failed, hit a cap, or went unread. See \`teams\`.`,
        }
      : {}),
  };
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

// `message send` accepts a user id as shorthand for "DM this person". Slack's
// org-level Grid token will not resolve that shorthand (it answers
// `channel_not_found`), so open the DM first and post to the conversation.
async function resolveChannel(creds, channel) {
  // Slack ids are uppercase; matching case-insensitively would swallow ordinary
  // channel names such as `watercooler`, which chat.postMessage accepts as-is.
  if (!/^[UW][A-Z0-9]{6,}$/.test(String(channel))) return channel;
  const opened = await webApiCall("conversations.open", { users: channel }, creds);
  if (!opened.channel?.id) {
    throw new Error(
      `Opened a DM with ${channel} but Slack returned no conversation id. Pass a channel id (C…/D…) instead.`,
    );
  }
  return opened.channel.id;
}

export async function messageSend(creds, channel, text, options = {}) {
  return webApiCall("chat.postMessage", {
    channel: await resolveChannel(creds, channel),
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
