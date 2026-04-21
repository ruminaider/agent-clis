import { callTool } from "./mcp.js";

// Coerces any non-string input to the empty string, which stripNullish will
// drop before the MCP call. Callers that want to send an explicit null (for
// Linear's "clear this value" semantics) must guard at the call site, e.g.:
//   assignee: input.assignee === null ? null : normalizeText(input.assignee)
function normalizeText(value) {
  return typeof value === "string" ? value.trim() : "";
}

// Returns undefined for unset inputs so the caller (stripNullish) drops them
// before the MCP call. Numeric MCP fields are not nullable, so we never need
// to pass an explicit null through.
function normalizeNumber(value) {
  if (value === null || value === undefined || value === "") return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

// Returns undefined for unset inputs. Linear's array fields (labels, links,
// blocks, etc.) are plain arrays in the schema, never nullable, so we never
// emit an explicit null here.
function normalizeList(value) {
  if (value === null || value === undefined || value === "") return undefined;
  if (Array.isArray(value)) {
    const items = value.map((v) => (typeof v === "string" ? v.trim() : "")).filter(Boolean);
    return items.length > 0 ? items : undefined;
  }
  const text = typeof value === "string" ? value.trim() : "";
  return text ? [text] : undefined;
}

function normalizeLinkList(value) {
  if (value === null || value === undefined || value === "") return undefined;
  const raw = Array.isArray(value) ? value : [value];
  const items = [];
  for (const entry of raw) {
    if (!entry || typeof entry !== "string") continue;
    const sepIndex = entry.indexOf("|");
    if (sepIndex === -1) {
      throw new Error(`--link expects "url|title" format, received: ${entry}`);
    }
    const url = entry.slice(0, sepIndex).trim();
    const title = entry.slice(sepIndex + 1).trim();
    if (!url || !title) {
      throw new Error(`--link requires a non-empty url and title, received: ${entry}`);
    }
    items.push({ url, title });
  }
  return items.length > 0 ? items : undefined;
}

// Strips undefined and empty strings but preserves explicit null so callers can
// send `field: null` to Linear MCP tools that accept null for "clear this value".
function stripNullish(arguments_ = {}) {
  return Object.fromEntries(Object.entries(arguments_).filter(([, value]) => value !== undefined && value !== ""));
}

async function call(name, arguments_) {
  return callTool(name, stripNullish(arguments_));
}

// Attachments
export async function getAttachment(id) {
  return call("get_attachment", { id: normalizeText(id) });
}

export async function createAttachment(input = {}) {
  return call("create_attachment", {
    issue: normalizeText(input.issue),
    base64Content: normalizeText(input.base64Content),
    filename: normalizeText(input.filename),
    contentType: normalizeText(input.contentType),
    title: normalizeText(input.title),
    subtitle: normalizeText(input.subtitle),
  });
}

export async function deleteAttachment(id) {
  return call("delete_attachment", { id: normalizeText(id) });
}

// Comments
export async function listComments(issueId, options = {}) {
  return call("list_comments", {
    issueId: normalizeText(issueId),
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function saveComment(input = {}) {
  return call("save_comment", {
    id: normalizeText(input.id),
    issueId: normalizeText(input.issueId),
    parentId: normalizeText(input.parentId),
    body: normalizeText(input.body),
  });
}

export async function deleteComment(id) {
  return call("delete_comment", { id: normalizeText(id) });
}

// Cycles
export async function listCycles(teamId, options = {}) {
  return call("list_cycles", {
    teamId: normalizeText(teamId),
    type: normalizeText(options.type),
  });
}

// Documents
export async function getDocument(id) {
  return call("get_document", { id: normalizeText(id) });
}

export async function listDocuments(options = {}) {
  return call("list_documents", {
    query: normalizeText(options.query),
    projectId: normalizeText(options.projectId),
    initiativeId: normalizeText(options.initiativeId),
    creatorId: normalizeText(options.creatorId),
    createdAt: normalizeText(options.createdAt),
    updatedAt: normalizeText(options.updatedAt),
    includeArchived: options.includeArchived,
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function createDocument(input = {}) {
  return call("create_document", {
    title: normalizeText(input.title),
    content: normalizeText(input.content),
    project: normalizeText(input.project),
    issue: normalizeText(input.issue),
    icon: normalizeText(input.icon),
    color: normalizeText(input.color),
  });
}

export async function updateDocument(input = {}) {
  return call("update_document", {
    id: normalizeText(input.id),
    title: normalizeText(input.title),
    content: normalizeText(input.content),
    project: normalizeText(input.project),
    icon: normalizeText(input.icon),
    color: normalizeText(input.color),
  });
}

// Images
export async function extractImages(markdown) {
  return call("extract_images", { markdown: normalizeText(markdown) });
}

// Issues
export async function getIssue(id, options = {}) {
  return call("get_issue", {
    id: normalizeText(id),
    includeRelations: options.includeRelations,
    includeCustomerNeeds: options.includeCustomerNeeds,
  });
}

export async function listIssues(options = {}) {
  return call("list_issues", {
    query: normalizeText(options.query),
    team: normalizeText(options.team),
    state: normalizeText(options.state),
    cycle: normalizeText(options.cycle),
    label: normalizeText(options.label),
    assignee: options.assignee === null ? null : normalizeText(options.assignee),
    delegate: normalizeText(options.delegate),
    project: normalizeText(options.project),
    priority: normalizeNumber(options.priority),
    parentId: normalizeText(options.parentId),
    createdAt: normalizeText(options.createdAt),
    updatedAt: normalizeText(options.updatedAt),
    includeArchived: options.includeArchived,
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function saveIssue(input = {}) {
  return call("save_issue", {
    id: normalizeText(input.id),
    title: normalizeText(input.title),
    description: normalizeText(input.description),
    team: normalizeText(input.team),
    cycle: normalizeText(input.cycle),
    milestone: normalizeText(input.milestone),
    priority: normalizeNumber(input.priority),
    project: normalizeText(input.project),
    state: normalizeText(input.state),
    assignee: input.assignee === null ? null : normalizeText(input.assignee),
    delegate: input.delegate === null ? null : normalizeText(input.delegate),
    labels: normalizeList(input.labels),
    dueDate: normalizeText(input.dueDate),
    parentId: input.parentId === null ? null : normalizeText(input.parentId),
    estimate: normalizeNumber(input.estimate),
    links: normalizeLinkList(input.links),
    blocks: normalizeList(input.blocks),
    blockedBy: normalizeList(input.blockedBy),
    relatedTo: normalizeList(input.relatedTo),
    duplicateOf: input.duplicateOf === null ? null : normalizeText(input.duplicateOf),
    removeBlocks: normalizeList(input.removeBlocks),
    removeBlockedBy: normalizeList(input.removeBlockedBy),
    removeRelatedTo: normalizeList(input.removeRelatedTo),
  });
}

// Issue statuses
export async function listIssueStatuses(team) {
  return call("list_issue_statuses", { team: normalizeText(team) });
}

export async function getIssueStatus(input = {}) {
  return call("get_issue_status", {
    id: normalizeText(input.id),
    name: normalizeText(input.name),
    team: normalizeText(input.team),
  });
}

// Issue labels
export async function listIssueLabels(options = {}) {
  return call("list_issue_labels", {
    name: normalizeText(options.name),
    team: normalizeText(options.team),
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function createIssueLabel(input = {}) {
  return call("create_issue_label", {
    name: normalizeText(input.name),
    description: normalizeText(input.description),
    color: normalizeText(input.color),
    teamId: normalizeText(input.teamId),
    parent: normalizeText(input.parent),
    isGroup: input.isGroup,
  });
}

// Projects
export async function listProjects(options = {}) {
  return call("list_projects", {
    query: normalizeText(options.query),
    state: normalizeText(options.state),
    initiative: normalizeText(options.initiative),
    team: normalizeText(options.team),
    member: normalizeText(options.member),
    label: normalizeText(options.label),
    createdAt: normalizeText(options.createdAt),
    updatedAt: normalizeText(options.updatedAt),
    includeMilestones: options.includeMilestones,
    includeMembers: options.includeMembers,
    includeArchived: options.includeArchived,
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function getProject(query, options = {}) {
  return call("get_project", {
    query: normalizeText(query),
    includeMilestones: options.includeMilestones,
    includeMembers: options.includeMembers,
    includeResources: options.includeResources,
  });
}

export async function saveProject(input = {}) {
  return call("save_project", {
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

export async function listProjectLabels(options = {}) {
  return call("list_project_labels", {
    name: normalizeText(options.name),
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

// Milestones
export async function listMilestones(project) {
  return call("list_milestones", { project: normalizeText(project) });
}

export async function getMilestone(project, query) {
  return call("get_milestone", {
    project: normalizeText(project),
    query: normalizeText(query),
  });
}

export async function saveMilestone(input = {}) {
  return call("save_milestone", {
    project: normalizeText(input.project),
    id: normalizeText(input.id),
    name: normalizeText(input.name),
    description: normalizeText(input.description),
    targetDate: input.targetDate === null ? null : normalizeText(input.targetDate),
  });
}

// Teams
export async function listTeams(options = {}) {
  return call("list_teams", {
    query: normalizeText(options.query),
    createdAt: normalizeText(options.createdAt),
    updatedAt: normalizeText(options.updatedAt),
    includeArchived: options.includeArchived,
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function getTeam(query) {
  return call("get_team", { query: normalizeText(query) });
}

// Users
export async function listUsers(options = {}) {
  return call("list_users", {
    query: normalizeText(options.query),
    team: normalizeText(options.team),
    limit: options.limit,
    cursor: options.cursor,
    orderBy: normalizeText(options.orderBy),
  });
}

export async function getUser(query) {
  return call("get_user", { query: normalizeText(query) });
}

// Documentation search
export async function searchDocumentation(query, options = {}) {
  return call("search_documentation", {
    query: normalizeText(query),
    page: options.page,
  });
}
