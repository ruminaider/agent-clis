// Metabase HTTP client, ported from the metabase-mcp-server: auth headers,
// exponential-backoff retry on transient failures, error classification, and
// email/password session auto-refresh on 401.

import { RETRY_ATTEMPTS } from "./config.js";

export class MetabaseError extends Error {
  constructor(message, status) {
    super(message);
    this.name = "MetabaseError";
    this.status = status;
  }
}

export class ReadOnlyError extends MetabaseError {
  constructor() {
    super("Write operations are disabled in read-only mode.", 0);
    this.name = "ReadOnlyError";
  }
}

const RETRYABLE_STATUS = new Set([429, 500, 502, 503, 504]);

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

export class MetabaseClient {
  // creds: { url, authMethod, apiKey, sessionToken, email, password, readOnly }
  constructor(creds) {
    this.creds = creds;
    this.sessionToken = creds.authMethod === "session-token" ? creds.sessionToken : null;
  }

  get readOnly() {
    return Boolean(this.creds.readOnly);
  }

  assertWriteEnabled() {
    if (this.creds.readOnly) throw new ReadOnlyError();
  }

  async get(path, params) {
    let url = `${this.creds.url}${path}`;
    if (params) {
      const sp = new URLSearchParams();
      for (const [key, value] of Object.entries(params)) {
        if (value === undefined || value === null) continue;
        // Repeated params (e.g. models) arrive as arrays.
        if (Array.isArray(value)) for (const v of value) sp.append(key, String(v));
        else sp.append(key, String(value));
      }
      const qs = sp.toString();
      if (qs) url += `?${qs}`;
    }
    return this.request("GET", url);
  }

  post(path, body) {
    return this.request("POST", `${this.creds.url}${path}`, body);
  }

  put(path, body) {
    return this.request("PUT", `${this.creds.url}${path}`, body);
  }

  delete(path) {
    return this.request("DELETE", `${this.creds.url}${path}`);
  }

  // Raw request that returns the Response (for binary/text exports).
  async requestRaw(method, path, body) {
    const headers = await this.authHeaders();
    const init = { method, headers };
    if (body !== undefined && method !== "GET") {
      headers["Content-Type"] = "application/json";
      init.body = JSON.stringify(body);
    }
    const res = await fetch(`${this.creds.url}${path}`, init);
    if (!res.ok) throw new MetabaseError(await this.errorMessage(res), res.status);
    return res;
  }

  // Form-encoded raw request. Metabase's export endpoints take the query as a
  // form field (not a JSON body) and return the file content.
  async requestRawForm(method, path, formObj) {
    const headers = await this.authHeaders();
    headers["Content-Type"] = "application/x-www-form-urlencoded";
    const body = new URLSearchParams();
    for (const [k, v] of Object.entries(formObj)) body.append(k, v);
    const res = await fetch(`${this.creds.url}${path}`, { method, headers, body });
    if (!res.ok) throw new MetabaseError(await this.errorMessage(res), res.status);
    return res;
  }

  async request(method, url, body, isRetryAfterAuth = false) {
    let attempt = 0;
    while (true) {
      attempt++;
      const headers = { "Content-Type": "application/json" };
      await this.addAuthHeaders(headers);
      const init = { method, headers };
      if (body !== undefined && method !== "GET") init.body = JSON.stringify(body);

      let res;
      try {
        res = await fetch(url, init);
      } catch (err) {
        if (attempt < RETRY_ATTEMPTS) {
          await sleep(250 * 2 ** (attempt - 1));
          continue;
        }
        throw new MetabaseError(`Network error: ${err.message}`, 0);
      }

      if (res.ok) {
        if (res.status === 204) return undefined;
        return res.json();
      }

      // Auto re-authenticate once on 401 for email/password auth.
      if (res.status === 401 && this.creds.authMethod === "email-password" && !isRetryAfterAuth) {
        await this.authenticate();
        return this.request(method, url, body, true);
      }

      if (RETRYABLE_STATUS.has(res.status) && attempt < RETRY_ATTEMPTS) {
        await sleep(250 * 2 ** (attempt - 1));
        continue;
      }

      throw new MetabaseError(await this.errorMessage(res), res.status);
    }
  }

  async addAuthHeaders(headers) {
    Object.assign(headers, await this.authHeaders());
  }

  async authHeaders() {
    if (this.creds.authMethod === "api-key" && this.creds.apiKey) {
      return { "x-api-key": this.creds.apiKey };
    }
    if (this.creds.authMethod === "session-token" || this.creds.authMethod === "email-password") {
      if (!this.sessionToken && this.creds.authMethod === "email-password") {
        await this.authenticate();
      }
      if (this.sessionToken) return { "X-Metabase-Session": this.sessionToken };
    }
    return {};
  }

  async authenticate() {
    if (this.creds.authMethod !== "email-password" || !this.creds.email || !this.creds.password) {
      throw new MetabaseError("Cannot authenticate: email/password not configured.", 401);
    }
    const res = await fetch(`${this.creds.url}/api/session`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ username: this.creds.email, password: this.creds.password }),
    });
    if (!res.ok) throw new MetabaseError(`Login failed: ${await this.errorMessage(res)}`, res.status);
    const data = await res.json();
    this.sessionToken = data.id;
    return this.sessionToken;
  }

  async errorMessage(res) {
    let body;
    try {
      body = await res.json();
    } catch {
      return `HTTP ${res.status}`;
    }
    if (typeof body === "string") return body;
    // Metabase error bodies vary: query failures carry `error` (singular) or a
    // `cause`/`via[].message`; validation errors carry `message` or `errors`.
    // Prefer a concise field over dumping the whole stack-trace body.
    const via = Array.isArray(body?.via) ? body.via[0] : null;
    const message =
      body?.message ||
      body?.error ||
      body?.cause ||
      via?.message ||
      via?.error ||
      (body?.errors && JSON.stringify(body.errors));
    return message ? String(message) : `HTTP ${res.status}`;
  }
}

// Concurrency-limited batch, mirroring the MCP's batchProcess. Each item runs
// independently so one failure does not abort the batch.
export async function batchProcess(items, fn, concurrency = 5) {
  const results = new Array(items.length);
  let next = 0;
  async function worker() {
    while (next < items.length) {
      const i = next++;
      try {
        results[i] = await fn(items[i], i);
      } catch (err) {
        results[i] = { error: err.message };
      }
    }
  }
  const workers = Array.from({ length: Math.min(concurrency, items.length) }, worker);
  await Promise.all(workers);
  return results;
}
