# Agent Tool Shed

CLI tools that replace MCP servers for AI agents. Same capabilities, fraction of the context, fully composable.

MCP servers bloat your agent's context with dozens of tool descriptions and prevent composability. These CLI tools give agents the same access through bash: searchable, pipeable, chainable.

## Tools

| Tool | Replaces | Status |
|------|----------|--------|
| [notion](./notion/) | Notion MCP Server | Ready |
| [linear](./linear/) | Linear MCP Server | MCP-synced read/write surface |
| [circleci-cli](./circleci-cli/) | CircleCI MCP Server | Scaffolded |
| [slack](./slack/) | Slack MCP Server | Planned |
| [metabase](./metabase/) | Metabase MCP Server | Planned |
| [newrelic](./newrelic/) | NewRelic MCP Server | Planned |

## Install

```bash
# Via npm (ready today)
npm i -g @ruminaider/notion-cli

# Linear publish target
# npm i -g @ruminaider/linear-cli

# Or clone and install all tools
git clone https://github.com/ruminaider/agent-clis.git
bash agent-clis/install.sh

# Or clone and install one tool
bash agent-clis/notion/install.sh
bash agent-clis/linear/install.sh
bash agent-clis/circleci-cli/install.sh
```

## Why CLI Tools

MCP servers consume 2,000 to 5,000 tokens of context for tool descriptions alone. A CLI skill consumes roughly 200 tokens, because the agent loads full instructions only when it needs them.

```
MCP approach:
  Agent context ← 46 tool schemas (metabase) + 22 tool schemas (notion) + ...
  Every turn, every prompt, whether you need them or not.

CLI approach:
  Agent context ← "notion: Search/read/write Notion pages" (one line)
  Agent loads full instructions only when it decides to use Notion.
```

CLI tools compose through standard pipes:
```bash
# MCP can't do this
notion-cli search "Q3 roadmap" | jq '.results[0].id' | xargs notion-cli fetch

# Chain across tools
notion-cli search "deploy checklist" | jq -r '.results[0].text' | slack-cli post "#deploys"
```

## Auth

Each tool handles its own authentication. Most use OAuth with a browser flow, and some tools also accept direct API keys for headless use:

```bash
notion-cli auth          # Refreshes token first, then opens browser if needed
slack-cli auth login     # Same pattern
linear-cli auth status   # Also supports --api-key and persisted credentials
```

You create no custom integrations, and most flows should not require admin permissions.

## For AI Agent Harnesses

Each tool includes a skill file ([Agent Skills standard](https://agentskills.io)) compatible with [pi](https://github.com/badlogic/pi-coding-agent), Claude Code, and any harness that supports the standard.

```
agent-clis/
├── notion/
│   ├── cli/              # The CLI tool
│   ├── skill/            # Agent skill (SKILL.md)
│   └── install.sh        # Standalone installer
├── linear/
│   ├── cli/
│   ├── skill/
│   └── install.sh
├── slack/
│   ├── cli/
│   ├── skill/
│   └── install.sh
└── install.sh            # Install all tools
```

## License

MIT
