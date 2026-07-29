# Slack CLI

Use Slack from the terminal as yourself. `slack-cli` reuses the session your Slack desktop app already holds, so there is no Slack app to create, no OAuth consent screen, and no admin approval. Read channels, search, send and edit messages, react, and work with files, pins, and canvases, all with JSON output that pipes into `jq` and other tools.

```bash
slack-cli auth login
slack-cli channel list | jq -r '.channels[].name'
slack-cli message send C0123ABCD "Deploy finished ✅"   # use a channel ID, not #name
slack-cli search messages "incident in:#ops after:2026-06-01"
```

## Quick start

```bash
bash slack/install.sh          # or: npm i -g @ruminaider/slack-cli
slack-cli auth login           # extracts credentials from the Slack desktop app
slack-cli auth status          # confirms the authenticated workspace
```

`auth login` reads the `xoxc` web token and the encrypted `xoxd` cookie from the local Slack app. On macOS the first run shows a one-time Keychain prompt ("node wants to use Slack Safe Storage"); click Allow. Credentials are stored at `~/.config/slack-cli/credentials.json` with `0600` permissions.

Requirements: the Slack desktop app signed in, on macOS or Linux. Automatic extraction reads the encrypted cookie store via the built-in `node:sqlite`, which is flag-free on Node 24+ (on Node 22–23 run with `--experimental-sqlite`). The `SLACK_TOKEN`/`SLACK_COOKIE` env vars and `auth import` work on Node 22+ without it.

On Linux, keyring-backed cookie stores (`v11`) are not auto-decrypted; use `auth import` there.

## How it authenticates

Slack's official MCP server and OAuth flow both require registering a Slack app, which shows users a consent screen and may need admin approval. `slack-cli` takes the other path: it authenticates as the Slack web client does, with the user's own session token plus the paired `d` cookie. The token is useless without the cookie, which is the built-in safety boundary.

This is the most native "act as me" model, with one tradeoff: session credentials rotate when Slack invalidates the desktop session, so re-run `auth login` if calls begin returning `invalid_auth`. It is intended for personal and internal agent use rather than a distributed, sanctioned integration.

Alternatives to automatic extraction:

```bash
# Headless / CI: provide the pair via environment
export SLACK_TOKEN=xoxc-...
export SLACK_COOKIE=xoxd-...
export SLACK_COOKIE_DS=...        # optional: d-s companion, Enterprise Grid / SSO

# Manual import from a copied devtools cURL, or directly
slack-cli auth import --curl '<curl from browser devtools>'
slack-cli auth import --token xoxc-... --cookie xoxd-... [--cookie-ds ...]
```

On Enterprise Grid (and some SSO setups) Slack also sets a `d-s` session cookie. Automatic extraction and the cURL import capture it when present; supply it explicitly with `--cookie-ds` or `SLACK_COOKIE_DS` when importing by hand.

## Enterprise Grid

Grid works the same as a regular workspace: run `auth login` and every command behaves as you would expect. Getting there takes an extra step behind the scenes, because Slack splits a Grid session into an org-level identity and one identity per workspace, and refuses whole categories of calls on the org-level one.

`auth login` therefore stores a token for every workspace in your org, minting the ones the desktop app did not cache by loading each workspace's web app with your existing cookie. Commands then route themselves: search and the user directory run at the org level, where they see everything, while channel and DM listings sweep each workspace and merge the results, deduplicating channels shared across workspaces.

```bash
slack-cli auth status                       # shows each workspace and which entry is org-level
slack-cli channel list                      # every channel across the org
slack-cli channel list --types im           # every open DM
slack-cli channel list --team recorahealth  # one workspace only
slack-cli channel list --all-teams          # sweep every signed-in workspace, org or not
```

A sweep reports what it covered under `teams`, and sets `partial: true` when any workspace failed, hit a cap, or went unread, so an incomplete list never passes for the whole org. Across workspaces `--limit` caps the merged result rather than sizing a page, and each cut-short workspace carries the cursor to resume from. Paging with `--cursor` belongs to a single workspace: pass `--team` alongside it.

Workspace-scoped writes stay explicit. `channel create` names its target instead of addressing an id, so on an org-level default it asks for `--team` rather than creating the channel in whichever workspace answered first.

## Usage

Every command prints JSON to stdout. Target a specific workspace with `--team <name|id|host>` (or `SLACK_TEAM`); the default is the Enterprise Grid org when you have one, otherwise the first signed-in workspace.

```bash
# Channels
slack-cli channel list --types public_channel,private_channel --limit 50
slack-cli channel history C0123ABCD --limit 20
slack-cli channel members C0123ABCD

# Messages and threads
slack-cli message send C0123ABCD "Hello" --thread-ts 1782854820.669419
slack-cli message reply C0123ABCD 1782854820.669419 "On it"
slack-cli message update C0123ABCD 1782854999.000100 "Edited"
slack-cli message delete C0123ABCD 1782854999.000100
slack-cli thread read C0123ABCD 1782854820.669419

# Search, people, reactions
slack-cli search messages "from:@alice in:#ops" --sort timestamp
slack-cli user info U0AS27192FJ
slack-cli reaction add C0123ABCD 1782854820.669419 white_check_mark

# Files, pins, canvases
slack-cli file list --channel C0123ABCD
slack-cli pin list C0123ABCD
slack-cli canvas get F0123CANVAS
```

Run `slack-cli help` for the complete command list.

## Architecture

- `cli/bin/slack.js` parses arguments and dispatches commands; all output is JSON.
- `cli/lib/extract.js` reads the `xoxc` tokens from the Slack app's LevelDB (the JSON is Snappy-compressed, so tokens are regexed out as verbatim literals and verified over the network) and decrypts the `xoxd` cookie from the Chromium SQLite store using the platform key (macOS Keychain `Slack Safe Storage`, PBKDF2-HMAC-SHA1 + AES-128-CBC).
- `cli/lib/auth.js` resolves credentials (explicit → env → persisted), enriches tokens via `auth.test`, and stores the result.
- `cli/lib/grid.js` completes an Enterprise Grid login: it asks the org token which workspaces you belong to and mints the missing per-workspace tokens from each workspace's web boot page.
- `cli/lib/api.js` calls the Slack web API, sending the token in the form body and the `d` cookie in the `Cookie` header against the workspace host. Calls Slack refuses on an org-level token retry against the workspace tokens, and channel listings sweep the org.

Zero runtime dependencies: everything uses Node built-ins (`node:sqlite`, `node:crypto`, `node:child_process`, `node:fs`).

## Agent skill

`skill/slack/SKILL.md` follows the [Agent Skills standard](https://agentskills.io) and works with pi, Claude Code, and any compatible harness.
