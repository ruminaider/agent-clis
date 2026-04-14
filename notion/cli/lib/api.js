import { callTool } from "./mcp.js";

// ─── Search ──────────────────────────────────────────────

export async function search(query, options = {}) {
  const args = { query, filters: {} };
  if (options.type === "user") {
    args.query_type = "user";
  } else {
    args.query_type = "internal";
  }
  if (options.limit) args.page_size = options.limit;
  return callTool("notion-search", args);
}

// ─── Pages ───────────────────────────────────────────────

export async function getPage(id) {
  return callTool("notion-fetch", { id });
}

export async function createPage(parentId, title, content) {
  const page = { properties: { title } };
  if (content) page.content = content;
  return callTool("notion-create-pages", {
    parent: { type: "page_id", page_id: parentId },
    pages: [page],
  });
}

export async function updatePage(pageId, options) {
  let result;

  if (options.title || options.properties) {
    const properties = options.properties || {};
    if (options.title) properties.title = options.title;
    result = await callTool("notion-update-page", {
      page_id: pageId,
      command: "update_properties",
      properties,
    });
  }

  if (options.content !== undefined) {
    result = await callTool("notion-update-page", {
      page_id: pageId,
      command: "replace_content",
      new_str: options.content,
      ...(options.allowDeletingContent ? { allow_deleting_content: true } : {}),
    });
  }

  return result;
}

export async function editPageContent(pageId, contentUpdates, options = {}) {
  return callTool("notion-update-page", {
    page_id: pageId,
    command: "update_content",
    content_updates: contentUpdates,
    ...(options.allowDeletingContent ? { allow_deleting_content: true } : {}),
  });
}

export async function movePages(pageIds, newParentId) {
  return callTool("notion-move-pages", {
    page_or_database_ids: pageIds,
    new_parent: { type: "page_id", page_id: newParentId },
  });
}

export async function duplicatePage(pageId) {
  return callTool("notion-duplicate-page", { page_id: pageId });
}

// ─── Databases ───────────────────────────────────────────

export async function createDatabase(parentId, schema, title) {
  const args = {
    parent: { type: "page_id", page_id: parentId },
    schema,
  };
  if (title) args.title = title;
  return callTool("notion-create-database", args);
}

export async function updateDataSource(dataSourceId, statements, options = {}) {
  const args = { data_source_id: dataSourceId, statements };
  if (options.title) args.title = options.title;
  return callTool("notion-update-data-source", args);
}

// ─── Views ───────────────────────────────────────────────

export async function createView(databaseId, dataSourceId, name, type, configure) {
  const args = { database_id: databaseId, data_source_id: dataSourceId, name, type };
  if (configure) args.configure = configure;
  return callTool("notion-create-view", args);
}

export async function updateView(viewId, updates) {
  const args = { view_id: viewId };
  if (updates.name) args.name = updates.name;
  if (updates.configure) args.configure = updates.configure;
  return callTool("notion-update-view", args);
}

// ─── Comments ────────────────────────────────────────────

export async function getComments(pageId) {
  return callTool("notion-get-comments", { page_id: pageId });
}

export async function addComment(pageId, text, options = {}) {
  const args = {
    page_id: pageId,
    rich_text: [{ type: "text", text: { content: text } }],
  };
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
