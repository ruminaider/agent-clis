---
name: slack
description: "Use when the user wants to work in Slack through `slack-cli`: authenticate, read channels and threads, search, send or edit messages, react, or manage files, pins, and canvases. Also use when the user explicitly asks for `slack-cli`. Do NOT use for building or deploying Slack apps (that is Slack's own `slack` CLI), or for unrelated local files."
compatibility: Requires the Slack desktop app signed in (macOS or Linux). Auto-extraction needs node:sqlite (Node 24+, or Node 22-23 with --experimental-sqlite); the SLACK_TOKEN/SLACK_COOKIE env vars and `auth import` work on Node 22+. Run `slack-cli auth login` to authenticate.
---

# Slack CLI

## Overview
`slack-cli` gives terminal access to Slack as the signed-in user. It reuses the credentials the Slack desktop app already holds (the `xoxc` web token and the `xoxd` `d` cookie), so there is no Slack app, no OAuth consent screen, and no admin approval. Output is JSON on stdout, so commands pipe into `jq` and into other tools.

## Core Philosophy
**Authenticate from the desktop session, not from an app.** `slack-cli auth login` extracts and verifies credentials from the local Slack app. The session is the user's own, so the CLI acts with exactly the user's permissions.

**Resolve identifiers before acting.** Slack APIs take IDs, not names. List channels or users first, then use the returned `id` (channels look like `C…`, DMs `D…`, users `U…`, message timestamps are the `ts` string).

**Read before write.** Fetch the message or channel first, then send, edit, react, or delete against the exact `channel` + `ts`.

**Never print tokens.** Treat the credentials file at `~/.config/slack-cli/credentials.json` as secret. Do not echo token or cookie values into logs or chat.

## Domain Mechanics
1. **Auth.** `slack-cli auth login` extracts from the Slack desktop app (first run triggers a one-time macOS Keychain prompt; click Allow). `auth status` lists authenticated workspaces. For headless use, set `SLACK_TOKEN` (xoxc) and `SLACK_COOKIE` (xoxd), plus optional `SLACK_COOKIE_DS` on Enterprise Grid. Manual import: `auth import --curl '<curl copied from devtools>'` or `auth import --token xoxc-... --cookie xoxd-... [--cookie-ds ...]`. `auth logout` clears stored credentials. On Enterprise Grid / SSO, Slack also sets a `d-s` cookie that extraction and cURL import capture automatically.
2. **Workspaces.** Extraction captures every signed-in workspace. Target one with `--team <name|id|host>` on any command, or set `SLACK_TEAM`. The default is the first workspace.
3. **Channels.** `channel list [--types public_channel,private_channel,mpim,im] [--limit N]`, `channel info <C…>`, `channel history <C…> [--limit N] [--oldest ts] [--latest ts]`, `channel members <C…>`, `channel join <C…>`, `channel create <name> [--private]`.
4. **Messages.** `message send <channel> <text> [--thread-ts ts] [--broadcast]`, `message reply <channel> <thread-ts> <text>`, `message update <channel> <ts> <text>`, `message delete <channel> <ts>`, `message schedule <channel> <text> --at <unix-ts>`. `<channel>` accepts a channel ID, a DM ID, or a user ID (opens the DM).
5. **Threads.** `thread read <channel> <thread-ts>` returns the parent plus replies.
6. **Search.** `search messages <query> [--sort score|timestamp] [--limit N]`, `search files <query>`, `search all <query>`. Query supports Slack search operators (`in:#channel`, `from:@user`, `after:YYYY-MM-DD`).
7. **People & reactions.** `user list`, `user info <U…>`, `user me`. `reaction add <channel> <ts> <emoji>` and `reaction remove <channel> <ts> <emoji>` (emoji name without colons, e.g. `white_check_mark`).
8. **Files, pins, canvases.** `file list [--channel C…]`, `file info <F…>`. `pin list <channel>`, `pin add <channel> <ts>`, `pin remove <channel> <ts>`. `canvas list [--channel C…]`, `canvas get <canvas-id>`.

*Judgment:* When the target is named but not identified, list first to resolve the ID rather than guessing. For writes, confirm the channel and `ts` from a prior read. Use `slack-cli help` for the full command reference.

## Common Mistakes
- Passing a channel name where a channel ID (`C…`) is required. List channels first and use `id`.
- Re-encoding the `xoxd` cookie. The CLI handles this; when importing, paste the cookie value verbatim (it is already percent-encoded).
- Posting to a busy shared channel during testing. Use your own DM (`message send <your-user-id> ...`) and delete afterward.
- Expecting credentials to last forever. Slack session tokens rotate when the desktop session is invalidated; re-run `auth login` if calls start returning `invalid_auth`.
- Treating this as Slack's app-development CLI. Building, running, or deploying Slack apps is a different tool (`slack` from slackapi); `slack-cli` only reads and writes workspace data.
- Sending message text that starts with a dash without an escape. Put such text after `--`: `slack-cli message send C0123 -- "-1 vs baseline"`.
- On Linux with a keyring-backed (`v11`) cookie store, auto-extraction cannot decrypt; use `slack-cli auth import` instead.
