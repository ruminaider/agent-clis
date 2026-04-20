import { callTool } from "./mcp.js";

function normalizeText(value) {
  return typeof value === "string" ? value.trim() : "";
}

function normalizeNumber(value) {
  if (value === null || value === undefined || value === "") return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : null;
}

function normalizeList(value) {
  if (value === null || value === undefined || value === "") return null;
  if (Array.isArray(value)) {
    const items = value.map(normalizeText).filter(Boolean);
    return items.length > 0 ? items : null;
  }
  if (typeof value === "string") {
    const items = value
      .split(",")
      .map((entry) => entry.trim())
      .filter(Boolean);
    return items.length > 0 ? items : null;
  }
  return [String(value)];
}

function stripNullish(arguments_ = {}) {
  return Object.fromEntries(Object.entries(arguments_).filter(([, value]) => value !== null && value !== undefined && value !== ""));
}

function parseJsonMaybe(value) {
  if (typeof value !== "string") return value;
  const text = value.trim();
  if (!text) return text;

  try {
    return JSON.parse(text);
  } catch {
    return text;
  }
}

function unwrapToolContent(result) {
  if (!result || typeof result !== "object") return result;
  if (!Array.isArray(result.content)) return result;

  const textItems = result.content.filter((item) => item?.type === "text" && typeof item.text === "string");
  if (textItems.length === 0) return result;
  if (textItems.length === 1) return parseJsonMaybe(textItems[0].text);
  return textItems.map((item) => parseJsonMaybe(item.text));
}

async function callParsedTool(name, arguments_ = {}) {
  return unwrapToolContent(await callTool(name, stripNullish(arguments_)));
}

export async function listProjects(options = {}) {
  return callParsedTool("list_projects", {
    query: options.query ?? null,
    state: options.state ?? null,
    initiative: options.initiative ?? null,
    team: options.team ?? null,
    member: options.member ?? null,
    label: options.label ?? null,
    createdAt: options.createdAt ?? null,
    updatedAt: options.updatedAt ?? null,
    includeMilestones: options.includeMilestones ?? null,
    includeMembers: options.includeMembers ?? null,
    includeArchived: options.includeArchived ?? null,
    limit: options.limit ?? null,
    cursor: options.cursor ?? null,
    orderBy: options.orderBy ?? null,
  });
}

export async function getProject(query, options = {}) {
  return callParsedTool("get_project", {
    query: normalizeText(query),
    includeMilestones: options.includeMilestones ?? null,
    includeMembers: options.includeMembers ?? null,
    includeResources: options.includeResources ?? null,
  });
}

export async function listComments(issueId, options = {}) {
  return callParsedTool("list_comments", {
    issueId: normalizeText(issueId),
    limit: options.limit ?? null,
    cursor: options.cursor ?? null,
    orderBy: options.orderBy ?? null,
  });
}

export async function saveProject(input = {}) {
  return callParsedTool("save_project", {
    id: normalizeText(input.id),
    name: normalizeText(input.name),
    icon: normalizeText(input.icon),
    color: normalizeText(input.color),
    summary: normalizeText(input.summary),
    description: normalizeText(input.description),
    state: normalizeText(input.state),
    startDate: normalizeText(input.startDate),
    startDateResolution: normalizeText(input.startDateResolution),
    targetDate: normalizeText(input.targetDate),
    targetDateResolution: normalizeText(input.targetDateResolution),
    priority: normalizeNumber(input.priority),
    addTeams: normalizeList(input.addTeams),
    removeTeams: normalizeList(input.removeTeams),
    setTeams: normalizeList(input.setTeams),
    labels: normalizeList(input.labels),
    lead: input.lead === null ? null : normalizeText(input.lead),
    addInitiatives: normalizeList(input.addInitiatives),
    removeInitiatives: normalizeList(input.removeInitiatives),
    setInitiatives: normalizeList(input.setInitiatives),
  });
}

export const API_CONTEXT = Object.freeze({
  callTool,
  normalizeText,
  normalizeNumber,
  normalizeList,
  stripNullish,
  unwrapToolContent,
});
