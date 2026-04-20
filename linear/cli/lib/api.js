import { CLI_NAME } from "./config.js";
import { callTool } from "./mcp.js";

function scaffold(feature) {
  throw new Error(`${CLI_NAME} scaffold: ${feature} is not implemented yet`);
}

export function createApiError(code, message, details = {}) {
  const error = new Error(message);
  error.code = code;
  error.details = details;
  return error;
}

export function createIdentifierReference({ id = null, key = null, name = null, source = "unknown" } = {}) {
  return Object.freeze({ id, key, name, source });
}

export function hasDirectIdentifier(reference = {}) {
  return typeof reference.id === "string" || typeof reference.key === "string";
}

function normalizeText(value) {
  return typeof value === "string" ? value.trim() : "";
}

function looksLikeUuid(value) {
  return /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i.test(value);
}

function looksLikeLinearKey(value) {
  return /^[A-Z0-9]+-\d+$/.test(value);
}

function normalizeReference(reference) {
  if (typeof reference === "string") {
    const value = normalizeText(reference);
    if (!value) return createIdentifierReference();
    if (looksLikeUuid(value)) {
      return createIdentifierReference({ id: value, source: "input" });
    }
    if (looksLikeLinearKey(value)) {
      return createIdentifierReference({ key: value, source: "input" });
    }
    return createIdentifierReference({ name: value, source: "input" });
  }
  if (!reference || typeof reference !== "object") {
    return createIdentifierReference();
  }
  return createIdentifierReference({
    id: normalizeText(reference.id) || null,
    key: normalizeText(reference.key) || null,
    name: normalizeText(reference.name) || null,
    source: normalizeText(reference.source) || "input",
  });
}

export async function resolveIdentifierReference(entityType, reference, resolver) {
  const normalized = normalizeReference(reference);
  if (hasDirectIdentifier(normalized)) {
    return normalized;
  }

  if (typeof resolver !== "function") {
    throw createApiError(
      "IDENTIFIER_RESOLUTION_UNAVAILABLE",
      `${entityType} name resolution is unavailable in this build`,
      { entityType, reference: normalized },
    );
  }

  return resolver(normalized);
}

export function throwAmbiguousMatch(entityType, query, matches = []) {
  throw createApiError(
    "AMBIGUOUS_MATCH",
    `${entityType} lookup is ambiguous. Pass a unique ID or key instead.`,
    { entityType, query, matches },
  );
}

export function throwNotFound(entityType, query) {
  throw createApiError(
    "NOT_FOUND",
    `${entityType} not found. Pass a valid ID, key, or exact name.`,
    { entityType, query },
  );
}

export const API_IDENTIFIER_STRATEGY = Object.freeze({
  acceptedInputs: ["id", "key"],
  directMatchFields: ["id", "key"],
  resolutionPolicy: "accept direct identifiers immediately; keep name-based lookup out of the shipped MVP; fail clearly when a direct identifier is missing",
});

function stripNullish(arguments_ = {}) {
  return Object.fromEntries(Object.entries(arguments_).filter(([, value]) => value !== null && value !== undefined && value !== ""));
}

function unwrapToolPayload(result, fallbackKey = null) {
  if (result && typeof result === "object") {
    if (Array.isArray(result)) return result;
    if (fallbackKey && Array.isArray(result[fallbackKey])) return result[fallbackKey];
    if (Array.isArray(result.items)) return result.items;
    if (Array.isArray(result.nodes)) return result.nodes;
    if (Array.isArray(result.data)) return result.data;
  }
  return result;
}

export async function getProjectByReference(reference) {
  const resolved = await resolveIdentifierReference("Project", reference, null);
  const arguments_ = resolved.id ? { id: resolved.id } : { key: resolved.key };
  const result = await callTool("get_project", arguments_);
  return unwrapToolPayload(result, "project");
}

async function resolveIssueReference(reference) {
  const query = normalizeReference(reference);
  if (hasDirectIdentifier(query)) return query;
  throw createApiError(
    "IDENTIFIER_RESOLUTION_UNAVAILABLE",
    "Issue name resolution is unavailable in this build",
    { entityType: "Issue", reference: query },
  );
}

export async function listProjects(options = {}) {
  const result = await callTool("list_projects", stripNullish({
    team: options.team ?? null,
    workspace: options.workspace ?? null,
    cursor: options.cursor ?? null,
    limit: options.limit ?? null,
    orderBy: options.orderBy ?? null,
  }));
  return unwrapToolPayload(result, "projects");
}

export async function getProject(reference) {
  return getProjectByReference(reference);
}

export async function listComments(issueReference, options = {}) {
  const resolvedIssue = await resolveIssueReference(issueReference);
  const result = await callTool("list_comments", stripNullish({
    issueId: resolvedIssue.id ?? null,
    issueKey: resolvedIssue.key ?? null,
    cursor: options.cursor ?? null,
    limit: options.limit ?? null,
    orderBy: options.orderBy ?? null,
  }));
  return unwrapToolPayload(result, "comments");
}

export const API_CONTEXT = Object.freeze({
  callTool,
});
