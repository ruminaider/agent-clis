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

interface IntentRef {
  intentId: string;
  paths: string[];
}

type TaskSource = "flag" | "env" | "pr" | "branch" | "detached" | "auto" | "";

interface BootstrapState {
  bootstrapped: boolean;
  resolvedTaskId: string | null;
  resolvedAgentId: string | null;
  resolvedTaskSource: TaskSource;
  autoAssigned: boolean;
  bootstrapPromise: Promise<void> | null;
  liveClaims: Map<string, IntentRef>;
  bashSnapshots: Map<string, Set<string>>;
}

// --- Helpers -------------------------------------------------------------

async function runLedger(args: string[]): Promise<{ stdout: string; stderr: string; code: number }> {
  try {
    const { stdout, stderr } = await exec("agent-ledger", args, {
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
    state.resolvedTaskSource = (process.env.AGENT_LEDGER_TASK_SOURCE ?? "") as TaskSource;
    state.autoAssigned = process.env.AGENT_LEDGER_AUTO_ASSIGNED === "1";
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

function buildAutoAssignedMarker({ by, parent, task, agent, effect }: { by: string; parent?: string | null; task?: string | null; agent?: string | null; effect?: string | null }): string {
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

// --- Extension entry -----------------------------------------------------

export default function (pi: ExtensionAPI) {
  const state: BootstrapState = {
    bootstrapped: false,
    resolvedTaskId: null,
    resolvedAgentId: null,
    resolvedTaskSource: "",
    autoAssigned: false,
    bootstrapPromise: null,
    liveClaims: new Map(),
    bashSnapshots: new Map(),
  };

  // Bootstrap lazily on first tool call so we do not slow down sessions
  // that never edit files.
  pi.on("tool_call", async (event, ctx) => {
    const toolName = normalizeToolName(event.toolName);

    if (!state.bootstrapped) {
      try {
        await bootstrapSession(state, "pi", "worker");
        // Notify only on the auto fallback path (no harness context
        // found). Branch/PR/detached/explicit sources are normal and
        // do not need a UI toast; the source is logged to stderr by
        // the bootstrap script and exposed via AGENT_LEDGER_TASK_SOURCE.
        if (state.resolvedTaskSource === "auto" && ctx.hasUI) {
          ctx.ui.notify(`agent-ledger: no task context found; auto task=${state.resolvedTaskId}`, "warning");
        }
      } catch (err: any) {
        if (process.env.AGENT_LEDGER_REQUIRE_TASK === "1") {
          return { block: true, reason: `agent-ledger bootstrap failed: ${err.message}` };
        }
        // Soft-fail: log and continue without ledger discipline.
        console.error(`agent-ledger bootstrap failed (continuing without enforcement): ${err.message}`);
        return undefined;
      }
    }

    // Subagent dispatch: assign a child task and inject env so the
    // child pi process picks up where the parent left off.
    if (SUBAGENT_TOOLS.has(toolName)) {
      const childAgent = (event.input?.agent as string) ?? "subagent";
      const childTask = `${state.resolvedTaskId}/${childAgent}/${Date.now().toString(36)}`;
      const policy = process.env.AGENT_LEDGER_AUTO_ASSIGN_POLICY ?? "warn";
      const allowArgs = splitAllowGlobs(process.env.AGENT_LEDGER_AUTO_ASSIGN_ALLOW).flatMap((g) => ["--allow", g]);
      const assign = await runLedger([
        "assign",
        "--task", childTask,
        "--orchestrator", state.resolvedAgentId ?? "pi-parent",
        "--agent", childAgent,
        "--policy", policy,
        ...allowArgs,
        "--reason", `${buildAutoAssignedMarker({ by: "pi-extension-subagent-hook", parent: state.resolvedTaskId, task: childTask, agent: childAgent })} subagent dispatch from ${state.resolvedAgentId ?? "pi"}`,
      ]);
      if (assign.code !== 0) {
        return { block: true, reason: `agent-ledger refused subagent assignment: ${assign.stderr.trim() || `exit ${assign.code}`}` };
      }
      if (event.input && typeof event.input === "object") {
        const env = (event.input.env as Record<string, string> | undefined) ?? {};
        env.AGENT_LEDGER_TASK_ID = childTask;
        env.AGENT_LEDGER_PARENT_TASK_ID = state.resolvedTaskId ?? "";
        if (process.env.AGENT_LEDGER_DIR) env.AGENT_LEDGER_DIR = process.env.AGENT_LEDGER_DIR;
        (event.input as any).env = env;
      }
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
  splitAllowGlobs,
};
