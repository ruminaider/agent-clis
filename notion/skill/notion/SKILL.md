---
name: notion
description: Use when the user needs to search, read, create, update, move, duplicate, remove child pages, or comment on Notion pages or databases from the terminal, or needs Notion-flavored Markdown content. Do NOT use for Claude Code plugin or MCP setup.
compatibility: Requires Node.js. Run `notion-cli auth` or `/notion-setup` in pi to authenticate.
---

# Notion CLI

## Overview
`notion-cli` gives terminal access to a full Notion workspace through Notion's remote MCP OAuth. Use it when the agent should act on Notion directly instead of asking for manual page sharing.

## Core Philosophy
**Fetch before editing.** Read the current page first, then decide whether a full-body update is safe.
**Use `page edit` for surgical changes.** Exact-match search and replace against existing page content.
**Reserve `page update --content` for full rewrites.** It replaces the whole body.
**Match sibling indentation when inserting content.** Tab depth determines block nesting in Notion. Missing tabs silently create top-level blocks.
*Judgment:* If a task only says "update the page," prefer `page edit` for targeted text changes. Use `page update --content` only when a full rewrite is intended.

## Domain Mechanics
1. Authenticate with `notion-cli auth`, then check `notion-cli auth status`. Credentials live at `~/.config/notion-cli/credentials.json`.
2. Search and fetch with `notion-cli search` and `notion-cli fetch`. Search is semantic; page IDs work with or without hyphens.
3. Manage pages with `page create`, `page edit`, `page update`, `page move`, `page duplicate`, and `page remove-child`. Use `--find`/`--replace` for short replacements, `--find-file`/`--replace-file` for multiline sections, `--edits-file` for batch changes. In batch JSON, set `replace_all_matches` per update. Use `--allow-deleting-content` when a replacement removes content.
4. Manage databases with `db create` and `db update` using `--schema` (`--ddl` is a fallback alias).
5. Handle collaboration with `comment list`, `comment add`, `users`, and `teams`.
*Judgment:* When the user asks for Notion structure, use `fetch` before guessing parent pages or database shape.

## Notion-flavored Markdown
Use standard Markdown plus Notion-specific blocks for toggles, callouts, columns, colors, tasks, equations, and tables. The full spec is at `notion://docs/enhanced-markdown-spec` via MCP resource. Fetch it before editing structurally complex pages.

**Indentation controls nesting.** Tab characters determine parent-child block relationships. Content inside a toggle heading must be tab-indented or it lands at the page's top level. Before inserting content, fetch the page and check the tab depth of sibling blocks at the insertion point. Match that depth in `new_str`.
```
## Section {toggle="true"}
\tThis paragraph is inside the toggle.
\t- This list item is inside the toggle.
This paragraph is NOT inside the toggle.
```
*Judgment:* If the fetched page uses block syntax, produce Notion-flavored Markdown for new content. Do not flatten to plain Markdown.

## Common Mistakes
Correct batch file with tab-indented replacement:
```json
[
  {
    "old_str": "- Old deliverable",
    "new_str": "\t\t- New deliverable inside a toggle"
  }
]
```

- Assuming `page update --content` appends (it replaces).
- Using `page update --content` for a targeted edit when `page edit` would be safer.
- Inserting content inside a toggle without matching the tab depth of siblings.
- Skipping the markdown spec fetch before editing pages with toggles, callouts, or nested structure.
- Using `--ddl` when `--schema` is preferred.
- Treating search as title-only instead of semantic.
- Passing database URLs where a parent page ID is required.
