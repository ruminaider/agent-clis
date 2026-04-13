---
name: notion
description: Interact with a Notion workspace via CLI. Search, read, create, and edit pages; manage databases, comments, users, and teams.
compatibility: Requires Node.js. Run /notion-setup or `notion-cli auth` to authenticate.
---

# Notion CLI

Access your full Notion workspace from the terminal with `notion-cli`. No integration setup, no page sharing required.

## Setup

```bash
notion-cli auth           # Refresh credentials, then open browser if needed
notion-cli auth status    # Check auth status
```

Credentials are stored at `~/.config/notion-cli/credentials.json`. You can also run `/notion-setup` in pi.

## Commands

### Search
```bash
notion-cli search "query"                    # Semantic search across workspace
```

### Fetch page/database content
```bash
notion-cli fetch <page-id>                   # Fetch by ID
notion-cli fetch "https://notion.so/..."     # Fetch by URL
```

### Pages
```bash
notion-cli page create --parent <id> --title "Title" [--content "markdown"]
notion-cli page update <page-id> [--title "New Title"] [--content "markdown"]
notion-cli page move <page-id> --parent <new-parent-id>
notion-cli page duplicate <page-id>
```

### Content formatting (Notion-flavored Markdown)

The `--content` flag accepts Notion-flavored Markdown, a superset of standard Markdown with XML extensions for Notion-specific blocks. Use this to create rich, well-formatted pages.

**Note:** `page update --content` replaces all existing page content.

Standard Markdown works as expected (headings, bold, italic, lists, code blocks, blockquotes, images, links, tables via pipe syntax). The extensions below cover Notion-specific features:

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

### Databases
```bash
notion-cli db create --parent <id> --title "Tasks" --schema "CREATE TABLE tasks (Name TEXT, Status SELECT('Todo','Done'))"
notion-cli db update <data-source-id> --schema "ALTER TABLE ..."
```

The `--schema` flag accepts SQL DDL syntax. `--ddl` still works as an alias.

### Comments
```bash
notion-cli comment list <page-id>            # List comments and discussions
notion-cli comment add <page-id> "text"      # Add a comment
```

### Users and Teams
```bash
notion-cli users                             # List workspace users
notion-cli teams                             # List workspace teams
```

### Debug
```bash
notion-cli tools                             # List available MCP tools
```

## Output

All commands return JSON. Pipe to `jq` for filtering:

```bash
notion-cli search "roadmap" | jq '.results[].title'
notion-cli users | jq '.results[] | select(.type=="person") | .name'
```

## Gotchas

- Page IDs work with or without hyphens.
- Search matches content, not just titles.
- If you get auth errors, run `notion-cli auth` to re-authenticate.
- `notion-cli auth status` shows token expiry and last refresh time.
