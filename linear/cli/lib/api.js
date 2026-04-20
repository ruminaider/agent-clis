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

export async function resolveIdentifierReference(entityType, reference, resolver) {
  if (hasDirectIdentifier(reference)) {
    return reference;
  }

  if (typeof resolver !== "function") {
    throw createApiError(
      "IDENTIFIER_RESOLUTION_UNAVAILABLE",
      `${CLI_NAME} scaffold: ${entityType} identifier resolution is not implemented yet`,
      { entityType, reference },
    );
  }

  return resolver(reference);
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
  acceptedInputs: ["id", "key", "name"],
  directMatchFields: ["id", "key"],
  resolutionPolicy: "accept direct identifiers immediately; resolve human-friendly names through tool calls later; fail clearly on ambiguity",
});

export async function listProjects() {
  return scaffold("listProjects");
}

export async function getProject() {
  return scaffold("getProject");
}

export async function listComments() {
  return scaffold("listComments");
}

export const API_CONTEXT = Object.freeze({
  callTool,
});
