/**
 * Agent Ledger pi extension.
 *
 * Wraps every pi tool call in claim/record discipline backed by the
 * agent-ledger CLI. See agent-ledger/docs/adapters.md for the env var
 * contract and auto-assignment design.
 *
 * Install: symlink this file into ~/.pi/agent/extensions/agent-ledger.ts.
 * Pi loads TypeScript extensions directly through its jiti-based loader,
 * so no build step is required for pi versions that support extensions.
 *
 * Requires: agent-ledger >= 0.1.0 on PATH; the project ledger
 * initialized via `agent-ledger init --write-pointer`.
 */

import type { ExtensionAPI } from "@mariozechner/pi-coding-agent";
import { execFile } from "node:child_process";
import { promisify } from "node:util";
import { promises as fs } from "node:fs";
import * as path from "node:path";

const exec = promisify(execFile);

// --- Configuration -------------------------------------------------------

const EDIT_TOOLS = new Set(["write", "edit", "multi_edit", "multiedit"]);
const BASH_TOOLS = new Set(["bash"]);
const SUBAGENT_TOOLS = new Set(["subagent"]);
const GIT_STATUS_MAX_BUFFER = 10 * 1024 * 1024;
const GIT_STATUS_PATH_LIMIT = 1000;

const KNOWN_TASK_SOURCES = new Set<TaskSource>([
  "flag",
  "env",
  "pr",
  "branch",
  "detached",
  "pointer",
  "auto",
  "subagent",
]);
function parseTaskSource(value: string | undefined): TaskSource | null {
  if (!value) return null;
  return KNOWN_TASK_SOURCES.has(value as TaskSource) ? (value as TaskSource) : null;
}

// AUTO_REASON_HINTS expands the bootstrap's machine-readable
// AGENT_LEDGER_TASK_AUTO_REASON tokens into human guidance the toast can
// render directly. Keep this map in sync with the AUTO_REASON tokens
// emitted by adapters/shared/session-bootstrap.sh AND with the byte-
// equivalent map in adapters/shared/auto-fallback-toast.js (whose tests
// assert the user-visible toast text). adapters/tests/run.sh enforces
// the parity statically.
const AUTO_REASON_HINTS: Record<string, string> = {
  not_in_git_repo:
    "cwd is not inside a git checkout. Set AGENT_LEDGER_TASK_ID, declare default_task_id in .agent-ledger.toml, or launch from inside a git checkout.",
  git_no_head:
    "git repo has no branch and no resolvable HEAD. Set AGENT_LEDGER_TASK_ID or declare default_task_id in .agent-ledger.toml.",
  pointer_lacks_default:
    "local .agent-ledger.toml does not declare default_task_id. Add it, or set AGENT_LEDGER_TASK_ID.",
  pointer_unreadable:
    "local .agent-ledger.toml exists but cannot be parsed; agent-ledger pointer show failed. Fix the file (run `agent-ledger pointer show` to see the error), or set AGENT_LEDGER_TASK_ID.",
  pointer_parser_unavailable:
    "local .agent-ledger.toml is present but neither python3 nor node is on PATH to parse the kernel's JSON projection. Install python3 or node, or set AGENT_LEDGER_TASK_ID.",
};

export function buildAutoFallbackToast(taskId: string | null, reason: string | null): string {
  const head = `agent-ledger: no task context found; auto task=${taskId ?? "<unknown>"}`;
  const hint = reason ? AUTO_REASON_HINTS[reason] : undefined;
  if (hint) return `${head} (${hint})`;
  if (reason) return `${head} (reason=${reason})`;
  return head;
}

interface IntentRef {
  intentId: string;
  paths: string[];
}

type TaskSource = "flag" | "env" | "pr" | "branch" | "detached" | "pointer" | "auto" | "subagent";

interface BootstrapState {
  bootstrapped: boolean;
  resolvedTaskId: string | null;
  resolvedAgentId: string | null;
  resolvedTaskSource: TaskSource | null;
  autoAssigned: boolean;
  // autoReason mirrors AGENT_LEDGER_TASK_AUTO_REASON from the bootstrap
  // when resolvedTaskSource === "auto". Used to render an actionable
  // toast hint. Always null for non-auto sources.
  autoReason: string | null;
  bootstrapPromise: Promise<void> | null;
  // Persists a fatal error from the eager child bootstrap path so the
  // first `tool_call` hook can observe it and block. The lazy
  // bootstrap path on the hook itself does not need this field because
  // its rejection is awaited synchronously inside the hook. See
  // `tasks/option-d-context.md` decision 3.
  eagerBootstrapError: Error | null;
  liveClaims: Map<string, IntentRef>;
  bashSnapshots: Map<string, Set<string>>;
}

// --- Helpers -------------------------------------------------------------

async function runLedger(args: string[], cwd = process.cwd()): Promise<{ stdout: string; stderr: string; code: number }> {
  try {
    const { stdout, stderr } = await exec("agent-ledger", args, {
      cwd,
      env: process.env,
      maxBuffer: 1024 * 1024,
    });
    return { stdout, stderr, code: 0 };
  } catch (err: any) {
    return {
      stdout: err.stdout?.toString() ?? "",
      stderr: err.stderr?.toString() ?? String(err.message ?? err),
      code: err.code ?? 1,
    };
  }
}

function decodeShellSingleQuoted(raw: string): string {
  const s = raw.trim();
  if (!s.startsWith("'") || !s.endsWith("'")) return s;
  return s.slice(1, -1).replace(/'\\''/g, "'");
}

function parseBootstrapOutput(stdout: string): Record<string, string> {
  for (const line of stdout.split("\n")) {
    if (!line.startsWith("AGENT_LEDGER_BOOTSTRAP_JSON=")) continue;
    return JSON.parse(line.slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
  }

  // Backwards-compatible parser for older bootstrap helpers that emitted
  // shell export lines.
  const env: Record<string, string> = {};
  for (const line of stdout.split("\n")) {
    const m = line.match(/^export\s+([A-Z_]+)=(.*)$/);
    if (!m) continue;
    const [, k, vRaw] = m;
    env[k] = decodeShellSingleQuoted(vRaw);
  }
  return env;
}

function errorMessage(err: any): string {
  return [err?.stderr?.toString().trim(), err?.message?.toString().trim()]
    .filter(Boolean)
    .join("\n");
}

function shouldBlockBootstrapFailure(): boolean {
  // Hard-fail in subagent child mode. The harness has identified this
  // process as a self-assigning child (see `adapters/shared/session-
  // bootstrap.sh` and `tasks/option-d-context.md` decision 3), so
  // ledger enforcement is mandatory: a misconfigured child must not
  // proceed to write files without a durable assignment row.
  if (process.env.PI_SUBAGENT_CHILD === "1") return true;
  return process.env.AGENT_LEDGER_REQUIRE_TASK === "1" || Boolean(process.env.AGENT_LEDGER_TASK_ID);
}

async function bootstrapSession(state: BootstrapState, harness: string, agentKind: string): Promise<void> {
  if (state.bootstrapped) return;
  if (state.bootstrapPromise) return state.bootstrapPromise;

  state.bootstrapPromise = (async () => {
    // Locate session-bootstrap.sh next to this extension.
    const here = path.dirname(new URL(import.meta.url).pathname);
    const candidates = [
      path.join(here, "session-bootstrap.sh"),
      path.join(here, "agent-ledger/session-bootstrap.sh"),
      path.join(here, "../shared/session-bootstrap.sh"),
      path.join(process.env.HOME ?? "", ".pi/agent/extensions/agent-ledger/session-bootstrap.sh"),
    ];
    let script: string | null = null;
    for (const p of candidates) {
      try { await fs.access(p); script = p; break; } catch { /* try next */ }
    }
    if (!script) {
      throw new Error(`agent-ledger extension: session-bootstrap.sh not found in ${candidates.join(", ")}`);
    }
    const args = [
      "--harness", harness,
      "--agent-kind", agentKind,
      "--orchestrator", "pi-extension",
      "--cwd", process.cwd(),
      "--json",
    ];
    if (process.env.AGENT_LEDGER_DETECT_PR === "1") {
      args.push("--detect-pr", "1");
    }
    const r = await exec("bash", [script, ...args], { env: process.env, maxBuffer: 1024 * 1024 });
    const exported = parseBootstrapOutput(r.stdout);
    for (const [k, v] of Object.entries(exported)) process.env[k] = v;
    state.resolvedAgentId = process.env.AGENT_ID ?? null;
    state.resolvedTaskId = process.env.AGENT_LEDGER_TASK_ID ?? null;
    state.resolvedTaskSource = parseTaskSource(process.env.AGENT_LEDGER_TASK_SOURCE);
    state.autoAssigned = process.env.AGENT_LEDGER_AUTO_ASSIGNED === "1";
    state.autoReason = process.env.AGENT_LEDGER_TASK_AUTO_REASON ?? null;
    state.bootstrapped = true;
  })();

  try {
    await state.bootstrapPromise;
  } finally {
    state.bootstrapPromise = null;
  }
}

function normalizeToolName(toolName: string | undefined): string {
  return String(toolName ?? "").toLowerCase();
}

function valuePath(input: any): string | undefined {
  const p = input?.path ?? input?.file_path ?? input?.filePath;
  return typeof p === "string" ? p : undefined;
}

function extractEditPaths(toolName: string, input: any): string[] {
  if (!input) return [];
  if (toolName === "multi_edit" || toolName === "multiedit") {
    const arr = Array.isArray(input.edits) ? input.edits : [];
    const set = new Set<string>();
    for (const e of arr) {
      const p = valuePath(e);
      if (p) set.add(p);
    }
    return [...set];
  }
  const p = valuePath(input);
  return p ? [p] : [];
}

function summaryFromInput(toolName: string, input: any): string {
  const reason = process.env.AGENT_LEDGER_REASON;
  if (reason) return reason;
  const p = valuePath(input) ?? "<unknown>";
  if (toolName === "write") return `pi write: ${p}`;
  if (toolName === "edit") return `pi edit: ${p}`;
  if (toolName === "multi_edit" || toolName === "multiedit") return "pi multi-edit";
  return `pi ${toolName}`;
}

function splitAllowGlobs(value: string | undefined): string[] {
  const raw = value ?? "**";
  return raw.split(":").map((g) => g.trim()).filter(Boolean);
}

function sanitizeMarkerToken(value: string): string {
  return String(value).replace(/[^A-Za-z0-9._:@/-]/g, "-");
}

// buildAssignmentMarker keeps byte-for-byte parity with the shared
// helpers in `adapters/shared/marker.sh` and `adapters/shared/marker.js`.
// Source values recognized as harness-derived produce the
// `[harness-derived by <by> source=<source> ...]` form. All other
// inputs (including the default and explicit `auto`) preserve the
// `[auto-assigned by <by> auto-derived ...]` form for backward
// compatibility with v0.2.0-rc1 marker readers.
//
// The authoritative metadata schema for subagent-created child
// assignment rows lives in `adapters/shared/marker.js` as the
// `SubagentAssignmentMetadata` JSDoc typedef. Bootstrap and verify
// must keep their structured assignment metadata payloads aligned
// with that schema; the reason-text marker emitted here is only an
// audit hint.
function buildAssignmentMarker({ by, parent, task, agent, effect, source }: { by: string; parent?: string | null; task?: string | null; agent?: string | null; effect?: string | null; source?: string | null }): string {
  const sourceTag = (source ?? "auto").toLowerCase();
  if (sourceTag === "subagent") {
    const parts = [`[harness-derived by ${sanitizeMarkerToken(by)}`, `source=${sanitizeMarkerToken(sourceTag)}`];
    if (parent) parts.push(`parent=${sanitizeMarkerToken(parent)}`);
    if (task) parts.push(`task=${sanitizeMarkerToken(task)}`);
    if (agent) parts.push(`agent=${sanitizeMarkerToken(agent)}`);
    if (effect) parts.push(`effect=${sanitizeMarkerToken(effect)}`);
    return `${parts.join(" ")}]`;
  }
  const parts = [`[auto-assigned by ${sanitizeMarkerToken(by)}`, "auto-derived"];
  if (parent) parts.push(`parent=${sanitizeMarkerToken(parent)}`);
  if (task) parts.push(`task=${sanitizeMarkerToken(task)}`);
  if (agent) parts.push(`agent=${sanitizeMarkerToken(agent)}`);
  if (effect) parts.push(`effect=${sanitizeMarkerToken(effect)}`);
  return `${parts.join(" ")}]`;
}

async function claimPaths(state: BootstrapState, paths: string[], reason: string): Promise<{ ok: boolean; intentId?: string; reason?: string }> {
  if (!state.resolvedTaskId) return { ok: false, reason: "AGENT_LEDGER_TASK_ID not resolved (bootstrap failed?)" };
  const args = ["claim", ...paths, "--task", state.resolvedTaskId, "--reason", reason, "--json"];
  const r = await runLedger(args);
  if (r.code !== 0) {
    return { ok: false, reason: r.stderr.trim() || `claim failed (exit ${r.code})` };
  }
  try {
    const parsed = JSON.parse(r.stdout);
    return { ok: true, intentId: parsed.intent_id };
  } catch {
    return { ok: false, reason: `claim succeeded but JSON parse failed: ${r.stdout.slice(0, 200)}` };
  }
}

async function recordPaths(paths: string[], intentId: string, summary: string): Promise<void> {
  const args = ["record", ...paths, "--intent", intentId, "--summary", summary];
  const r = await runLedger(args);
  if (r.code !== 0) {
    // Record failure is logged but does not crash the agent.
    console.error(`agent-ledger: record failed (intent=${intentId}): ${r.stderr.trim()}`);
  }
}

async function gitStatusPaths(): Promise<Set<string>> {
  try {
    const r = await exec("git", ["status", "--porcelain=v1"], {
      env: process.env,
      maxBuffer: GIT_STATUS_MAX_BUFFER,
    });
    const paths = r.stdout
      .split("\n")
      .filter((l) => l.length > 0)
      .map((l) => l.slice(3).split(" -> ").pop()!.trim())
      .filter(Boolean);
    if (paths.length > GIT_STATUS_PATH_LIMIT) {
      console.error(`agent-ledger: git status returned ${paths.length} paths; recording first ${GIT_STATUS_PATH_LIMIT}`);
    }
    return new Set(paths.slice(0, GIT_STATUS_PATH_LIMIT));
  } catch (err: any) {
    console.error(`agent-ledger: git status scan failed: ${err.message ?? err}`);
    return new Set();
  }
}

function diffPaths(after: Set<string>, before: Set<string>): string[] {
  return [...after].filter((p) => !before.has(p));
}

function isExecutionSubagentCall(input: any): boolean {
  return !input?.action;
}

// --- Extension entry -----------------------------------------------------

export default function (pi: ExtensionAPI) {
  const state: BootstrapState = {
    bootstrapped: false,
    resolvedTaskId: null,
    resolvedAgentId: null,
    resolvedTaskSource: null,
    autoAssigned: false,
    autoReason: null,
    bootstrapPromise: null,
    eagerBootstrapError: null,
    liveClaims: new Map(),
    bashSnapshots: new Map(),
  };

  // Eager child bootstrap. When pi-subagents spawns this process as a
  // subagent child, run the bootstrap immediately at extension load so
  // the child's assignment row exists before any tool call. This keeps
  // audit chronology clean (`task.assigned` precedes any later
  // `intent.opened`) and ensures zero-tool children still leave a row.
  // Subsequent `tool_call` hooks see `state.bootstrapped === true` and
  // skip re-bootstrapping. See `tasks/option-d-context.md` decision 2.
  if (process.env.PI_SUBAGENT_CHILD === "1") {
    void bootstrapSession(state, "pi", "worker").catch((err) => {
      const message = errorMessage(err);
      console.error(`agent-ledger eager child bootstrap failed: ${message}`);
      // Persist the failure so the first `tool_call` hook can observe
      // it and block. Without this the hook would race with the
      // already-rejected eager promise, see `state.bootstrapped` is
      // false, run the lazy path, and likely succeed or fail in a way
      // the operator cannot connect back to the eager rejection. See
      // decision 3 in `tasks/option-d-context.md`.
      state.eagerBootstrapError = err instanceof Error ? err : new Error(message);
    });
  }

  // Bootstrap lazily on first tool call so we do not slow down sessions
  // that never edit files. In subagent child mode the eager bootstrap
  // above usually wins this race; the lazy call below then awaits the
  // already in-flight bootstrap promise.
  pi.on("tool_call", async (event, ctx) => {
    const toolName = normalizeToolName(event.toolName);

    // Eager child bootstrap already failed fatally. Surface the
    // failure on the first tool call rather than silently retrying
    // (which would just fail the same way and obscure the original
    // diagnostic). Only blocks under `shouldBlockBootstrapFailure()`
    // semantics, which include `PI_SUBAGENT_CHILD=1`.
    if (state.eagerBootstrapError && shouldBlockBootstrapFailure()) {
      return {
        block: true,
        reason: `agent-ledger bootstrap failed: ${errorMessage(state.eagerBootstrapError)}`,
      };
    }

    if (!state.bootstrapped) {
      try {
        await bootstrapSession(state, "pi", "worker");
        // Notify only on the auto fallback path (no harness context
        // found). Branch/PR/detached/explicit sources are normal and
        // do not need a UI toast; the source is logged to stderr by
        // the bootstrap script and exposed via AGENT_LEDGER_TASK_SOURCE.
        if (state.resolvedTaskSource === "auto" && ctx.hasUI) {
          ctx.ui.notify(buildAutoFallbackToast(state.resolvedTaskId, state.autoReason), "warning");
        }
      } catch (err: any) {
        const message = errorMessage(err);
        if (shouldBlockBootstrapFailure()) {
          return { block: true, reason: `agent-ledger bootstrap failed: ${message}` };
        }
        // Soft-fail only when no explicit task contract was supplied.
        console.error(`agent-ledger bootstrap failed (continuing without enforcement): ${message}`);
        return undefined;
      }
    }

    // Subagent dispatch: observation-only.
    //
    // Children self-assign through their own bootstrap when
    // `PI_SUBAGENT_CHILD=1`. The parent extension does not mint child
    // task ids, does not call `agent-ledger assign`, and does not
    // mutate `process.env` for any subagent dispatch. The hook stays
    // here so future cross-cutting concerns (telemetry, correlation,
    // audit hints) have a single attachment point. See
    // `tasks/option-d-context.md` decision 4.
    if (SUBAGENT_TOOLS.has(toolName)) {
      if (!isExecutionSubagentCall(event.input)) return undefined;
      const childAgent = (event.input?.agent as string) ?? "subagent";
      console.error(
        `agent-ledger: subagent dispatch parent_task=${state.resolvedTaskId ?? ""} child_agent=${childAgent} dispatched_at=${new Date().toISOString()}`,
      );
      return undefined;
    }

    // Edit tools: claim paths before the edit runs.
    if (EDIT_TOOLS.has(toolName)) {
      const paths = extractEditPaths(toolName, event.input);
      if (paths.length === 0) return undefined;
      const summary = summaryFromInput(toolName, event.input);
      const claim = await claimPaths(state, paths, summary);
      if (!claim.ok) {
        return { block: true, reason: `agent-ledger refused claim: ${claim.reason}` };
      }
      state.liveClaims.set(event.toolCallId, { intentId: claim.intentId!, paths });
      return undefined;
    }

    // Bash: warn-only by default; configurable via AGENT_LEDGER_BASH_MODE=block.
    // Block mode is an advisory deny list, not a complete shell sandbox.
    if (BASH_TOOLS.has(toolName)) {
      const mode = process.env.AGENT_LEDGER_BASH_MODE ?? "warn";
      if (mode === "block") {
        return { block: true, reason: "agent-ledger blocks bash in block mode because shell mutation detection is not complete (set AGENT_LEDGER_BASH_MODE=warn to allow post-scan attribution)" };
      }
      state.bashSnapshots.set(event.toolCallId, await gitStatusPaths());
      return undefined;
    }

    return undefined;
  });

  pi.on("tool_result", async (event, _ctx) => {
    if (!state.bootstrapped) return;
    const toolName = normalizeToolName(event.toolName);

    // Edit tools: record against the intent claimed in tool_call.
    if (EDIT_TOOLS.has(toolName)) {
      const live = state.liveClaims.get(event.toolCallId);
      if (!live) return;
      state.liveClaims.delete(event.toolCallId);
      if (event.isError) return; // no record on error
      const summary = summaryFromInput(toolName, event.input ?? {});
      await recordPaths(live.paths, live.intentId, summary);
      return;
    }

    // Bash: post-scan working tree and attribute only paths that became
    // dirty during this bash call, not the entire pre-existing dirty tree.
    if (BASH_TOOLS.has(toolName)) {
      const before = state.bashSnapshots.get(event.toolCallId) ?? new Set<string>();
      state.bashSnapshots.delete(event.toolCallId);
      if (event.isError) return;
      const dirty = diffPaths(await gitStatusPaths(), before);
      if (dirty.length === 0) return;
      const claim = await claimPaths(state, dirty, `pi bash: ${String(event.input?.command ?? "").slice(0, 80)}`);
      if (claim.ok && claim.intentId) {
        await recordPaths(dirty, claim.intentId, "bash-observed change");
      }
    }
  });

  pi.on("agent_end", async () => {
    // Best-effort intent close at session end. Open intents are
    // tolerated (verify reports OPEN_INTENT as severity info), so we
    // just leave them as-is here. A future iteration can enumerate
    // open intents for this session and close them automatically.
  });
}

export {
  decodeShellSingleQuoted,
  diffPaths,
  extractEditPaths,
  normalizeToolName,
  parseBootstrapOutput,
  errorMessage,
  isExecutionSubagentCall,
  parseTaskSource,
  shouldBlockBootstrapFailure,
  splitAllowGlobs,
};
