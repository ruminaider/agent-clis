import { callTool } from "./mcp.js";

// ─── Search ──────────────────────────────────────────────

export async function search(query, options = {}) {
  const args = { query };
  if (options.type === "internal" || options.type === "external") {
    args.search_type = options.type;
  } else {
    args.search_type = "internal";
  }
  if (options.limit) args.limit = options.limit;
  return callTool("notion-search", args);
}

// ─── Pages ───────────────────────────────────────────────

export async function getPage(id) {
  return callTool("notion-fetch", { id });
}

export async function createPage(parentId, title, content) {
  const args = {
    pages: [{
      parent_id: parentId,
      title,
    }],
  };
  if (content) {
    args.pages[0].content_markdown = content;
  }
  return callTool("notion-create-pages", args);
}

export async function updatePage(pageId, properties) {
  const args = { page_id: pageId };
  if (properties.title) args.title = properties.title;
  if (properties.content) args.content_markdown = properties.content;
  if (properties.properties) args.properties = properties.properties;
  return callTool("notion-update-page", args);
}

export async function movePages(pageIds, newParentId) {
  return callTool("notion-move-pages", {
    page_ids: pageIds,
    new_parent_id: newParentId,
  });
}

export async function duplicatePage(pageId) {
  return callTool("notion-duplicate-page", { page_id: pageId });
}

// ─── Databases ───────────────────────────────────────────

export async function createDatabase(parentId, ddl) {
  return callTool("notion-create-database", {
    parent_id: parentId,
    ddl,
  });
}

export async function updateDataSource(dataSourceId, ddl) {
  return callTool("notion-update-data-source", {
    data_source_id: dataSourceId,
    ddl,
  });
}

// ─── Views ───────────────────────────────────────────────

export async function createView(databaseId, viewConfig) {
  return callTool("notion-create-view", {
    database_id: databaseId,
    ...viewConfig,
  });
}

export async function updateView(viewId, updates) {
  return callTool("notion-update-view", {
    view_id: viewId,
    ...updates,
  });
}

// ─── Comments ────────────────────────────────────────────

export async function getComments(pageId) {
  return callTool("notion-get-comments", { page_id: pageId });
}

export async function addComment(pageId, text, options = {}) {
  const args = { page_id: pageId, body: text };
  if (options.discussion_id) args.discussion_id = options.discussion_id;
  return callTool("notion-create-comment", args);
}

// ─── Users & Teams ───────────────────────────────────────

export async function listUsers() {
  return callTool("notion-get-users", {});
}

export async function listTeams() {
  return callTool("notion-get-teams", {});
}
