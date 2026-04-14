---
name: notion
description: Use when the user needs to search, read, create, update, move, duplicate, or comment on Notion pages or databases from the terminal, or needs Notion-flavored Markdown content. Do NOT use for Claude Code plugin or MCP setup.
compatibility: Requires Node.js. Run `notion-cli auth` or `/notion-setup` in pi to authenticate.
---

# Notion CLI

## Overview
`notion-cli` gives terminal access to a full Notion workspace through Notion's remote MCP OAuth. Use it when Claude should act on Notion directly instead of asking for manual page sharing.

## Core Philosophy
**Prefer the CLI, not the MCP plugin**, because the CLI already handles OAuth, tool wiring, and JSON output.
**Treat `auth` as the default entrypoint.** `notion-cli auth` refreshes first, then opens the browser if needed.
**Use `page edit` for surgical replacements.** It performs exact-match search and replace against existing page content.
**Reserve `page update --content` for full rewrites.** It replaces the whole body.
*Judgment:* If a task only says "update the page", prefer `page edit` when the user wants a targeted text change, and use `page update --content` only when a full rewrite is intended.

## Domain Mechanics
1. Authenticate with `notion-cli auth`, then check `notion-cli auth status` if needed. Credentials live at `~/.config/notion-cli/credentials.json`.
2. Search and fetch with `notion-cli search` and `notion-cli fetch`. Search is semantic, and page IDs work with or without hyphens.
3. Manage pages with `page create`, `page edit`, `page update`, `page move`, `page duplicate`, and `page remove-child`. Use `--find` and `--replace` for short exact replacements, `--find-file` and `--replace-file` for multiline sections, and `--edits-file` for batch changes. Use `page remove-child --force` when a parent page embeds a child page reference that should be removed through Notion's child-page deletion flow. In batch JSON, set `replace_all_matches` per update, and use `--allow-deleting-content` when a replacement deletes content, including an empty string.
4. Manage databases with `db create` and `db update`. Prefer `--schema`; `--ddl` still works as a fallback. `db create` can take an optional `--title`.
5. Handle collaboration with `comment list`, `comment add`, `users`, `teams`, and `tools`. `comment list` can also include child-block discussions, resolved threads, or one specific discussion via flags.
*Judgment:* When the user asks for Notion structure, use `fetch` before guessing parent pages or database shape.

## Notion-flavored Markdown
Use standard Markdown plus Notion-specific blocks for toggles, callouts, columns, colors, tasks, equations, and tables. Nested blocks must be tab-indented.
*Judgment:* If the content needs Notion-specific blocks, produce Notion-flavored Markdown, not plain Markdown. Prefer `page edit` when only part of the body changes, and remember that `page update --content` replaces the full body.

## Common Mistakes
Example batch file:
```json
[
  {
    "old_str": "## Old Section\nOld content here",
    "new_str": "## Old Section\nNew content here"
  }
]
```

- Using `--ddl` when `--schema` is preferred.
- Assuming `page update --content` appends.
- Using `page update --content` for a targeted edit when `page edit` or `page remove-child` would be safer.
- Using `--all` with `--edits-file`, or forgetting that batch replacements use `replace_all_matches` inside the JSON file.
- Treating search as title-only instead of semantic.
- Passing database URLs where a parent page ID is required.
- Forgetting that IDs work with or without hyphens.
- Failing to pipe JSON to `jq` when filtering results.
