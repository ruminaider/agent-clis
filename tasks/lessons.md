# Lessons

- Distinguish commit messages from PR descriptions. For commit messages, prefer a concise one-line subject unless the user explicitly asks for a body. Put fuller explanatory detail in the PR description or release notes instead.
- When the user asks whether Pi and Claude Code skills are updated, do not infer from the repo skill alone. Check the installed Pi skill and the installed Claude Code skill separately, and answer each part explicitly.
- Do not commit generated context artifacts like `context.md`. Add them to `.gitignore` before they can be staged or committed.
- Do not silently swallow missing runtime dependencies for user-visible behavior. If browser launch is best-effort, log a clear fallback message and ensure required packages are declared in package.json.
- When a user reports a CLI behaves differently in a brand new terminal, verify behavior in a clean shell environment before assuming the installed binary and current shell behave the same.
- When installing skills across harnesses, check for native command or plugin name collisions first. Prefer removing redundant wrappers in Claude Code and renaming Codex skills if a built-in or plugin already owns the shorter name.
- When a user asks to update an existing PR in a specific repo, do not infer a different target repo from the most recent local file change. Use the repo and PR thread the user named, then fix the PR body there.

## linear-cli: nullability regression during --clear-* implementation

### What went wrong
When adding `--clear-*` flags that pass literal null to MCP, I relaxed `stripNullish` in `cli/lib/api.js` to preserve nulls. But `getFlag` returned `null` for missing flags, `normalizeText`/`normalizeList`/`normalizeNumber` all returned `null` for unset inputs, and most api.js wrappers used `options.x ?? null`. Missing flags started reaching the MCP server as explicit nulls, which rejected them with "expected string" / "expected array" / "expected boolean" errors across almost every write command.

### Rule to prevent recurrence
When introducing "explicit null" semantics into a pipeline that previously stripped all nullish values, distinguish between three states up front:
1. **Not provided**: should be stripped from the MCP payload. Represent as `undefined` at every layer.
2. **Explicit clear**: should pass through as a literal `null`. Represent as `null` only when the user requested it (a `--clear-*` flag, etc.).
3. **Explicit value**: passes through as-is.

Concretely for this CLI:
- `getFlag`, `getBooleanFlag`, `asInteger`, `assertChoice` must return `undefined` for missing input, never `null`.
- `normalizeText` preserves explicit null, returns empty string (which `stripNullish` drops) otherwise.
- `normalizeNumber`, `normalizeList`, `normalizeLinkList` never emit `null` (those MCP fields are not nullable); they return `undefined` for missing input.
- `stripNullish` keeps `null` and drops only `undefined` and `""`.

### Detection
End-to-end scripted exercise against a real workspace caught this immediately after the clear-flag refactor. Unit-level CLI parsing tests alone would have missed it. For any change that affects the shape of outbound payloads, run at least one real MCP call per write path before calling the change done.
