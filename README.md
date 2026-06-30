# Agent Tool Shed

CLI tools that replace MCP servers for AI agents. Same capabilities, fraction of the context, fully composable.

MCP servers bloat your agent's context with dozens of tool descriptions and prevent composability. These CLI tools give agents the same access through bash: searchable, pipeable, chainable.

## Tools

### MCP replacements

| Tool | Replaces | Status |
|------|----------|--------|
| [notion](./notion/) | Notion MCP Server | Ready |
| [linear](./linear/) | Linear MCP Server | MCP-synced read/write surface |
| [circleci-cli](./circleci-cli/) | CircleCI MCP Server | Ready |
| [slack](./slack/) | Slack MCP Server | Ready |
| [metabase](./metabase/) | Metabase MCP Server | Planned |
| [newrelic](./newrelic/) | NewRelic MCP Server | Planned |

### Coordination kernel

| Tool | Purpose | Status |
|------|---------|--------|
| [agent-ledger](./agent-ledger/) | Local ledger that records which agent claimed which files, what changed, and whether changes stayed in scope. Harness-neutral, with adapters for pi (stable) and Babysitter (experimental). | v0.2.0 |

`agent-ledger` does not replace an MCP server. It sits next to the others and gives any agent harness a coordination layer: assignments, file claims, change records, and a verifiable `agent-ledger.verify.v1` contract per task.

## Install

```bash
# Via npm (ready today)
npm i -g @ruminaider/notion-cli

# Linear publish target
# npm i -g @ruminaider/linear-cli

# agent-ledger ships as a Go binary (requires Go 1.22+)
go install github.com/ruminaider/agent-clis/agent-ledger/cmd/agent-ledger@latest

# Or clone and install all tools
git clone https://github.com/ruminaider/agent-clis.git
bash agent-clis/install.sh

# Or clone and install one tool
bash agent-clis/notion/install.sh
bash agent-clis/linear/install.sh
bash agent-clis/slack/install.sh
bash agent-clis/circleci-cli/install.sh
```

For `agent-ledger` release archives and source builds, see [`agent-ledger/README.md`](./agent-ledger/README.md).

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
linear-cli auth status   # Also supports --api-key and persisted credentials
slack-cli auth login     # Reuses your Slack desktop session: no app, no consent screen
```

Most tools create no custom integrations, and most flows should not require admin permissions. `slack-cli` is the exception in approach: rather than an OAuth app, it authenticates as the Slack web client does, using the `xoxc` token and `xoxd` cookie your signed-in desktop app already holds (see [`slack/README.md`](./slack/README.md)).

`agent-ledger` needs no auth: it is a local-only tool that writes to `$XDG_STATE_HOME/agent-ledger/` (overridable via `AGENT_LEDGER_DIR`).

## For AI Agent Harnesses

Each tool includes a skill file ([Agent Skills standard](https://agentskills.io)) compatible with [pi](https://github.com/badlogic/pi-coding-agent), Claude Code, and any harness that supports the standard.

`agent-ledger` goes one step further. Its `adapters/pi/` and `adapters/babysitter/` wrappers translate harness events into ledger calls (`assign`, `claim`, `record`, `close`, `verify`) so the agent does not have to remember to coordinate. The cross-harness env contract lives in `agent-ledger/docs/adapters.md`.

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
├── agent-ledger/         # Go-based coordination kernel
│   ├── cmd/agent-ledger/ # Binary entrypoint
│   ├── adapters/         # pi (stable), babysitter (experimental)
│   └── docs/             # Walkthrough, exit codes, finding codes
└── install.sh            # Install all tools
```

## License

MIT
