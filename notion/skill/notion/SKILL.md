---
name: notion
description: Use when the user needs to read, create, edit, search, or manage Notion pages, databases, comments, users, or teams from the terminal. Also use when building automations or scripts that interact with a Notion workspace.
compatibility: Requires Node.js. Run `notion-cli auth` to authenticate.
---

# Notion CLI

Access your full Notion workspace from the terminal. No integration setup, no page sharing required. Run `notion-cli auth --help` for details.

## Quick Reference

| Task | Command |
|------|---------|
| Search workspace | `notion-cli search "query"` |
| Read a page | `notion-cli fetch <page-id-or-url>` |
| Create a page | `notion-cli page create --parent <id> --title "Title" [--content "md"]` |
| Update a page | `notion-cli page update <id> [--title "T"] [--content "md"]` |
| Move a page | `notion-cli page move <id> --parent <new-parent-id>` |
| Duplicate a page | `notion-cli page duplicate <id>` |
| Create a database | `notion-cli db create --parent <id> --schema "CREATE TABLE ..." [--title "T"]` |
| Update a database | `notion-cli db update <data-source-id> --schema "ALTER TABLE ..."` |
| List comments | `notion-cli comment list <page-id>` |
| Add a comment | `notion-cli comment add <page-id> "text"` |
| List users | `notion-cli users` |
| List teams | `notion-cli teams` |

All commands return JSON. Pipe to `jq` for filtering.

## Content Formatting (Notion-flavored Markdown)

The `--content` flag accepts Notion-flavored Markdown: standard Markdown plus XML extensions for Notion-specific blocks. `page update --content` replaces all existing content.

Standard Markdown works as expected (headings, bold, italic, lists, code blocks, blockquotes, images, links). The extensions below cover Notion-specific features:

```markdown
## Toggle heading {toggle="true"}
	Indented children appear inside the toggle.

<details>
<summary>Toggle block title</summary>
	Content inside the toggle (must be indented with tabs).
</details>

<callout icon="💡" color="blue_bg">
	Important information here.
	Callouts support multiple blocks as children.
</callout>

<columns>
	<column>
		Left column content
	</column>
	<column>
		Right column content
	</column>
</columns>

Text with {color="blue"} block color.
<span color="red">Inline colored text</span>
<span color="yellow_bg">Highlighted text</span>

- [ ] Unchecked to-do
- [x] Checked to-do

$$
E = mc^2
$$

<table header-row="true">
	<tr>
		<td>Header 1</td>
		<td>Header 2</td>
	</tr>
	<tr>
		<td>Cell A</td>
		<td>Cell B</td>
	</tr>
</table>
```

Colors: gray, brown, orange, yellow, green, blue, purple, pink, red (add `_bg` suffix for background colors).

Children (toggle content, callout body, list sub-items) must be indented with tabs.

## Common Mistakes

- Using `--ddl` instead of `--schema` for database commands. `--ddl` still works but `--schema` is preferred.
- Forgetting that `page update --content` replaces all content, not appends.
- Passing a database URL as `--parent` for page creation. Use the page ID of the parent page, not a database URL.
- Not quoting markdown content that contains shell-special characters.
- Search is semantic (content matching), not keyword-based on titles alone.
- Page IDs work with or without hyphens.
- Auth errors: run `notion-cli auth` to re-authenticate.
