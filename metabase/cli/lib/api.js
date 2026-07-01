// All Metabase operations, ported 1:1 from the metabase-mcp-server service
// layer. Each function takes a MetabaseClient and returns response-optimized
// data (list/detail/query shaping). Export functions return the raw Response so
// the caller can stream text or write binary. Write operations call
// client.assertWriteEnabled() so read-only mode blocks them.

import { optimizeDetail, optimizeList, optimizeQueryResult } from "./response.js";
import { batchProcess, MetabaseError } from "./client.js";
import { MAX_BATCH_CONCURRENCY } from "./config.js";

// Metabase's query endpoints stream failures as an HTTP 200 body carrying
// `status: "failed"` (or a `_status` with a Java stack trace) rather than an
// error status. Surface a concise error instead of dumping the whole trace.
function assertQueryOk(result) {
  if (!result || typeof result !== "object") return result;
  const failed = result.status === "failed" || result.error || (result._status && result._status >= 400);
  if (failed) {
    const message = result.error || result.cause || (Array.isArray(result.via) && result.via[0]?.message) || "Query failed";
    throw new MetabaseError(String(message), result._status || 400);
  }
  return result;
}

// ─── Cards (saved questions) ─────────────────────────────────
export async function listCards(client, { f, modelId } = {}) {
  const params = {};
  if (f) params.f = f;
  if (modelId) params.model_id = modelId;
  return optimizeList(await client.get("/api/card", params));
}

export async function getCard(client, ids) {
  if (ids.length === 1) return optimizeDetail(await client.get(`/api/card/${ids[0]}`));
  const results = await batchProcess(ids, (id) => client.get(`/api/card/${id}`), MAX_BATCH_CONCURRENCY);
  return optimizeDetail(results);
}

export async function createCard(client, params) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post("/api/card", params));
}

export async function updateCard(client, id, updates) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/card/${id}`, updates));
}

export async function copyCard(client, id) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post(`/api/card/${id}/copy`));
}

export async function executeCard(client, id, { parameters, ignoreCache } = {}) {
  const body = {};
  if (parameters) body.parameters = parameters;
  if (ignoreCache) body.ignore_cache = true;
  return optimizeQueryResult(assertQueryOk(await client.post(`/api/card/${id}/query`, body)));
}

export function exportCardResults(client, id, format) {
  return client.requestRawForm("POST", `/api/card/${id}/query/${format}`, {});
}

export async function getCardMetadata(client, id) {
  return optimizeDetail(await client.get(`/api/card/${id}/query_metadata`));
}

export async function listCardDashboards(client, id) {
  return optimizeDetail(await client.get(`/api/card/${id}/dashboards`));
}

export async function archiveCard(client, id) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/card/${id}`, { archived: true }));
}

// ─── Dashboards ──────────────────────────────────────────────
export async function listDashboards(client, { f } = {}) {
  const params = {};
  if (f) params.f = f;
  return optimizeList(await client.get("/api/dashboard", params));
}

export async function getDashboard(client, ids) {
  if (ids.length === 1) return optimizeDetail(await client.get(`/api/dashboard/${ids[0]}`));
  const results = await batchProcess(ids, (id) => client.get(`/api/dashboard/${id}`), MAX_BATCH_CONCURRENCY);
  return optimizeDetail(results);
}

export async function createDashboard(client, params) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post("/api/dashboard", params));
}

export async function updateDashboard(client, id, updates) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/dashboard/${id}`, updates));
}

export async function copyDashboard(client, id, options = {}) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post(`/api/dashboard/${id}/copy`, options));
}

export async function updateDashboardCards(client, id, cards) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/dashboard/${id}/cards`, { cards }));
}

export async function archiveDashboard(client, id) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/dashboard/${id}`, { archived: true }));
}

export async function getDashboardMetadata(client, id) {
  return optimizeDetail(await client.get(`/api/dashboard/${id}/query_metadata`));
}

// ─── Databases ───────────────────────────────────────────────
export async function listDatabases(client, { includeCards } = {}) {
  return optimizeList(await client.get("/api/database", includeCards ? { include_cards: "true" } : {}));
}

export async function getDatabase(client, id) {
  return optimizeDetail(await client.get(`/api/database/${id}`));
}

export async function getDatabaseMetadata(client, id) {
  return optimizeDetail(await client.get(`/api/database/${id}/metadata`));
}

export async function listDatabaseSchemas(client, id) {
  return optimizeList(await client.get(`/api/database/${id}/schemas`));
}

export async function listSchemaTables(client, id, schema) {
  return optimizeList(await client.get(`/api/database/${id}/schema/${encodeURIComponent(schema)}`));
}

// ─── Tables & fields ─────────────────────────────────────────
export async function listTables(client) {
  return optimizeList(await client.get("/api/table"));
}

export async function getTable(client, ids) {
  if (ids.length === 1) return optimizeDetail(await client.get(`/api/table/${ids[0]}`));
  const results = await batchProcess(ids, (id) => client.get(`/api/table/${id}`), MAX_BATCH_CONCURRENCY);
  return optimizeDetail(results);
}

export async function getTableMetadata(client, id, { includeSensitiveFields } = {}) {
  const params = {};
  if (includeSensitiveFields) params.include_sensitive_fields = "true";
  return optimizeDetail(await client.get(`/api/table/${id}/query_metadata`, params));
}

export async function getTableFks(client, id) {
  return optimizeDetail(await client.get(`/api/table/${id}/fks`));
}

export async function getField(client, id) {
  return optimizeDetail(await client.get(`/api/field/${id}`));
}

export async function getFieldValues(client, id) {
  return optimizeDetail(await client.get(`/api/field/${id}/values`));
}

export async function updateField(client, id, updates) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/field/${id}`, updates));
}

// ─── Queries ─────────────────────────────────────────────────
export async function executeQuery(client, databaseId, query, templateTags) {
  const datasetQuery = {
    database: databaseId,
    type: "native",
    native: { query, "template-tags": templateTags ?? {} },
  };
  return optimizeQueryResult(assertQueryOk(await client.post("/api/dataset", datasetQuery)));
}

export function exportQueryResults(client, databaseId, query, format) {
  const datasetQuery = { database: databaseId, type: "native", native: { query } };
  return client.requestRawForm("POST", `/api/dataset/${format}`, { query: JSON.stringify(datasetQuery) });
}

export async function convertToNativeSql(client, datasetQuery) {
  return client.post("/api/dataset/native", { query: datasetQuery });
}

// ─── Collections ─────────────────────────────────────────────
export async function listCollections(client, { archived } = {}) {
  return optimizeList(await client.get("/api/collection", archived ? { archived: "true" } : {}));
}

export async function getCollection(client, id) {
  return optimizeDetail(await client.get(`/api/collection/${id}`));
}

export async function getCollectionItems(client, id, { models, limit, offset } = {}) {
  const params = {};
  if (models) params.models = models;
  if (limit) params.limit = limit;
  if (offset !== undefined && offset !== null) params.offset = offset;
  return optimizeList(await client.get(`/api/collection/${id}/items`, params));
}

export async function getCollectionTree(client) {
  return optimizeList(await client.get("/api/collection/tree"));
}

export async function createCollection(client, params) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post("/api/collection", params));
}

export async function updateCollection(client, id, updates) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.put(`/api/collection/${id}`, updates));
}

// ─── Search & activity ───────────────────────────────────────
export async function search(client, { q, models, archived, limit, offset } = {}) {
  const params = {};
  if (q) params.q = q;
  if (models) params.models = models;
  if (archived) params.archived = "true";
  if (limit) params.limit = limit;
  if (offset) params.offset = offset;
  return optimizeList(await client.get("/api/search", params));
}

export async function getRecentViews(client) {
  // Metabase requires a `context` (v0.62+): ask for recently viewed items.
  return optimizeList(await client.get("/api/activity/recents", { context: "views" }));
}

export async function getCurrentUser(client) {
  return optimizeDetail(await client.get("/api/user/current"));
}

export async function invalidateCache(client, { database, dashboard } = {}) {
  client.assertWriteEnabled();
  const params = {};
  if (database) params.db = database;
  if (dashboard) params.dashboard = dashboard;
  return client.post(`/api/cache/invalidate${buildQuery(params)}`);
}

function buildQuery(params) {
  const sp = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined) sp.append(k, String(v));
  const qs = sp.toString();
  return qs ? `?${qs}` : "";
}

// ─── Revisions & bookmarks ───────────────────────────────────
export async function getRevisions(client, entity, id) {
  return optimizeDetail(await client.get("/api/revision", { entity, id }));
}

export async function revertRevision(client, entity, id, revisionId) {
  client.assertWriteEnabled();
  return optimizeDetail(await client.post("/api/revision/revert", { entity, id, revision_id: revisionId }));
}

export async function toggleBookmark(client, model, id, action) {
  client.assertWriteEnabled();
  if (action === "create") {
    await client.post(`/api/bookmark/${model}/${id}`);
    return { ok: true, added: { model, id } };
  }
  await client.delete(`/api/bookmark/${model}/${id}`);
  return { ok: true, removed: { model, id } };
}
