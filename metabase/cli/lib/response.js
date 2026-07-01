// Response optimization, ported from the metabase-mcp-server.
// Strips heavy metadata fields and flattens query results to cut token usage.
// This shaping is what keeps the tool's output cheap for agents to consume.

// Fields always stripped from any Metabase API response.
const GLOBAL_STRIP_FIELDS = new Set([
  "result_metadata",
  "metabase_version",
  "enable_embedding",
  "embedding_params",
  "cache_invalidated_at",
  "cache_ttl",
  "made_public_by_id",
  "public_uuid",
  "entity_id",
  "can_run_adhoc_query",
  "can_restore",
  "can_delete",
  "can_manage_db",
  "moderation_reviews",
  "parameter_usage_count",
  "archived_directly",
  "collection_preview",
  "card_schema",
  "is_write",
]);

// Additional fields stripped from list responses (more aggressive).
const LIST_STRIP_FIELDS = new Set([
  ...GLOBAL_STRIP_FIELDS,
  "visualization_settings",
  "dataset_query",
  "parameters",
  "parameter_mappings",
  "creator",
  "last_query_start",
  "last_used_at",
  "dashboard_count",
  "collection_position",
  "creator_id",
  "view_count",
  "query_average_duration",
  "last-edit-info",
]);

function stripFields(obj, fieldsToStrip) {
  if (obj === null || obj === undefined) return obj;
  if (Array.isArray(obj)) return obj.map((item) => stripFields(item, fieldsToStrip));
  if (typeof obj !== "object") return obj;

  const result = {};
  for (const [key, value] of Object.entries(obj)) {
    if (fieldsToStrip.has(key)) continue;
    result[key] =
      typeof value === "object" && value !== null ? stripFields(value, fieldsToStrip) : value;
  }
  return result;
}

// Detail response (get_card, get_dashboard, ...): strip metadata bloat, keep
// functional fields like queries and parameters.
export function optimizeDetail(data) {
  return stripFields(data, GLOBAL_STRIP_FIELDS);
}

// List response: aggressive stripping down to identifiers and essentials.
export function optimizeList(data) {
  return stripFields(data, LIST_STRIP_FIELDS);
}

// Query execution: flatten Metabase's {data:{rows:[[..]],cols:[{name}]}} into
// [{col: value}], keeping row_count and status.
export function optimizeQueryResult(data) {
  if (data && typeof data === "object" && "data" in data) {
    const dataset = data.data;
    if (dataset && Array.isArray(dataset.rows) && Array.isArray(dataset.cols)) {
      const colNames = dataset.cols.map((c) => c.name);
      const rows = dataset.rows.map((row) => {
        const obj = {};
        for (let i = 0; i < colNames.length; i++) obj[colNames[i]] = row[i];
        return obj;
      });
      return { row_count: data.row_count, status: data.status, rows };
    }
  }
  return data;
}
