# 🏚️ Agent Tool Shed

**CLI tools that replace MCP servers for AI agents.** Same capabilities, fraction of the context, fully composable.

MCP servers bloat your agent's context with dozens of tool descriptions and aren't composable. These CLI tools give agents the same access through bash — searchable, pipeable, chainable.

## Tools

| Tool | Replaces | Status |
|------|----------|--------|
| [notion](./notion/) | Notion MCP Server | ✅ Ready |
| [slack](./slack/) | Slack MCP Server | 🔜 Planned |
| [metabase](./metabase/) | Metabase MCP Server | 🔜 Planned |
| [newrelic](./newrelic/) | NewRelic MCP Server | 🔜 Planned |

## Install

```bash
# Via npm (recommended)
npm i -g @ruminaider/notion-cli

# Or clone and install all tools
git clone https://github.com/ruminaider/agent-clis.git
bash agent-clis/install.sh

# Or clone and install one tool
bash agent-clis/notion/install.sh
```

## Why?

MCP servers typically consume **2,000–5,000 tokens** of context just for tool descriptions. A CLI skill consumes **~200 tokens** — the agent loads full instructions on-demand only when needed.

```
MCP approach:
  Agent context ← 46 tool schemas (metabase) + 22 tool schemas (notion) + ...
  Every turn, every prompt, whether you need them or not.

CLI approach:
  Agent context ← "notion: Search/read/write Notion pages" (one line)
  Agent loads full instructions only when it decides to use Notion.
```

CLI tools are also composable:
```bash
# MCP can't do this
notion-cli search "Q3 roadmap" | jq '.results[0].id' | xargs notion-cli fetch

# Chain across tools
notion-cli search "deploy checklist" | jq -r '.results[0].text' | slack-cli post "#deploys"
```

## Auth

Each tool handles its own authentication. Most use OAuth with a simple browser flow:

```bash
notion-cli auth login    # Opens browser → click Approve → done
slack-cli auth login     # Same pattern
```

No API keys to manage, no integrations to create, no admin permissions needed.

## For AI Agent Harnesses

Each tool includes a **skill** file ([Agent Skills standard](https://agentskills.io)) that works with [pi](https://github.com/badlogic/pi-coding-agent), Claude Code, and any harness that supports the standard.

```
agent-clis/
├── notion/
│   ├── cli/              # The CLI tool
│   ├── skill/            # Agent skill (SKILL.md)
│   └── install.sh        # Standalone installer
├── slack/
│   ├── cli/
│   ├── skill/
│   └── install.sh
└── install.sh            # Install all tools
```

## License

MIT
