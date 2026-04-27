/**
 * Agent Ledger pi extension.
 *
 * Wraps every pi tool call in claim/record discipline backed by the
 * agent-ledger CLI. See agent-ledger/docs/adapters.md for the env var
 * contract and auto-assignment design.
 *
 * Install: copy or symlink this file (or its compiled .js form) into
 * ~/.pi/agent/extensions/agent-ledger.ts.
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

const EDIT_TOOLS = new Set(["write", "edit", "multi_edit", "multiEdit"]);
const BASH_TOOLS = new Set(["bash"]);
const SUBAGENT_TOOLS = new Set(["subagent"]);

interface IntentRef {
  intentId: string;
  paths: string[];
}

// Tool-call-id keyed map of claims taken during a tool_call hook so the
// matching tool_result hook can record against the same intent.
const liveClaims = new Map<string, IntentRef>();

// Cached bootstrap state to avoid running the shell helper twice.
let bootstrapped = false;
let resolvedTaskId: string | null = null;
let resolvedAgentId: string | null = null;
let autoAssigned = false;

// --- Helpers -------------------------------------------------------------

async function runLedger(args: string[], opts: { input?: string } = {}): Promise<{ stdout: string; stderr: string; code: number }> {
  try {
    const { stdout, stderr } = await exec("agent-ledger", args, {
      env: process.env,
      maxBuffer: 1024 * 1024,
      input: opts.input,
    } as any);
    return { stdout, stderr, code: 0 };
  } catch (err: any) {
    return {
      stdout: err.stdout?.toString() ?? "",
      stderr: err.stderr?.toString() ?? String(err.message ?? err),
      code: err.code ?? 1,
    };
  }
}

async function bootstrapSession(harness: string, agentKind: string): Promise<void> {
  if (bootstrapped) return;
  // Locate session-bootstrap.sh next to this extension.
  const here = path.dirname(new URL(import.meta.url).pathname);
  const candidates = [
    path.join(here, "../shared/session-bootstrap.sh"),
    path.join(here, "session-bootstrap.sh"),
    path.join(process.env.HOME ?? "", ".pi/agent/extensions/agent-ledger/session-bootstrap.sh"),
  ];
  let script: string | null = null;
  for (const p of candidates) {
    try { await fs.access(p); script = p; break; } catch { /* try next */ }
  }
  if (!script) {
    throw new Error(`agent-ledger extension: session-bootstrap.sh not found in ${candidates.join(", ")}`);
  }
  const args = ["--harness", harness, "--agent-kind", agentKind, "--orchestrator", "pi-extension"];
  const r = await exec("bash", [script, ...args], { env: process.env });
  // Parse exports.
  for (const line of r.stdout.split("\n")) {
    const m = line.match(/^export\s+([A-Z_]+)=(.*)$/);
    if (!m) continue;
    const [, k, vRaw] = m;
    const v = vRaw.replace(/^['"]/, "").replace(/['"]$/, "");
    process.env[k] = v;
  }
  resolvedAgentId = process.env.AGENT_ID ?? null;
  resolvedTaskId = process.env.AGENT_LEDGER_TASK_ID ?? null;
  autoAssigned = process.env.AGENT_LEDGER_AUTO_ASSIGNED === "1";
  bootstrapped = true;
}

function extractEditPaths(toolName: string, input: any): string[] {
  if (!input) return [];
  if (toolName === "multi_edit" || toolName === "multiEdit") {
    const arr = Array.isArray(input.edits) ? input.edits : [];
    const set = new Set<string>();
    for (const e of arr) if (typeof e?.path === "string") set.add(e.path);
    return [...set];
  }
  if (typeof input.path === "string") return [input.path];
  return [];
}

function summaryFromInput(toolName: string, input: any): string {
  const reason = process.env.AGENT_LEDGER_REASON;
  if (reason) return reason;
  if (toolName === "write") return `pi write: ${input?.path ?? "<unknown>"}`;
  if (toolName === "edit") return `pi edit: ${input?.path ?? "<unknown>"}`;
  if (toolName === "multi_edit" || toolName === "multiEdit") return "pi multi-edit";
  return `pi ${toolName}`;
}

async function claimPaths(paths: string[], reason: string): Promise<{ ok: boolean; intentId?: string; reason?: string }> {
  if (!resolvedTaskId) return { ok: false, reason: "AGENT_LEDGER_TASK_ID not resolved (bootstrap failed?)" };
  const args = ["claim", ...paths, "--task", resolvedTaskId, "--reason", reason, "--json"];
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

async function gitStatusPaths(): Promise<string[]> {
  try {
    const r = await exec("git", ["status", "--porcelain=v1"], { env: process.env });
    return r.stdout
      .split("\n")
      .map((l) => l.trim())
      .filter((l) => l.length > 0)
      .map((l) => l.slice(3).split(" -> ").pop()!.trim())
      .filter(Boolean);
  } catch {
    return [];
  }
}

// --- Extension entry -----------------------------------------------------

export default function (pi: ExtensionAPI) {
  // Bootstrap lazily on first tool call so we do not slow down sessions
  // that never edit files.
  pi.on("tool_call", async (event, ctx) => {
    const toolName = (event.toolName || "").toLowerCase();

    if (!bootstrapped) {
      try {
        await bootstrapSession("pi", "worker");
        if (autoAssigned && ctx.hasUI) {
          ctx.ui.notify(`agent-ledger: orchestrator did not pre-assign; auto task=${resolvedTaskId}`, "warning");
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
      const childTask = `${resolvedTaskId}/${childAgent}/${Date.now().toString(36)}`;
      // Best-effort: write a child assignment with the parent allow set inherited.
      const policy = process.env.AGENT_LEDGER_AUTO_ASSIGN_POLICY ?? "warn";
      const allow = process.env.AGENT_LEDGER_AUTO_ASSIGN_ALLOW ?? "**";
      await runLedger([
        "assign",
        "--task", childTask,
        "--orchestrator", resolvedAgentId ?? "pi-parent",
        "--agent", childAgent,
        "--policy", policy,
        "--allow", allow,
        "--reason", `subagent dispatch from ${resolvedAgentId ?? "pi"}`,
        "--metadata", JSON.stringify({ auto_assigned: true, auto_assigned_by: "pi-extension-subagent-hook", parent_task: resolvedTaskId }),
      ]);
      // Inject env into the subagent invocation. Pi forwards process.env
      // by default; we set per-call vars in the input if the schema
      // supports it. As a best-effort fallback, set process.env so
      // synchronous child spawns inherit. The main effect is that the
      // child pi extension's bootstrap will pick up these values.
      if (event.input && typeof event.input === "object") {
        const env = (event.input.env as Record<string, string> | undefined) ?? {};
        env.AGENT_LEDGER_TASK_ID = childTask;
        env.AGENT_LEDGER_PARENT_TASK_ID = resolvedTaskId ?? "";
        env.AGENT_LEDGER_DIR = process.env.AGENT_LEDGER_DIR ?? "";
        (event.input as any).env = env;
      }
      return undefined;
    }

    // Edit tools: claim paths before the edit runs.
    if (EDIT_TOOLS.has(toolName)) {
      const paths = extractEditPaths(toolName, event.input);
      if (paths.length === 0) return undefined;
      const summary = summaryFromInput(toolName, event.input);
      const claim = await claimPaths(paths, summary);
      if (!claim.ok) {
        return { block: true, reason: `agent-ledger refused claim: ${claim.reason}` };
      }
      liveClaims.set(event.toolCallId, { intentId: claim.intentId!, paths });
      return undefined;
    }

    // Bash: warn-only by default; configurable via AGENT_LEDGER_BASH_MODE=block.
    if (BASH_TOOLS.has(toolName)) {
      const mode = process.env.AGENT_LEDGER_BASH_MODE ?? "warn";
      if (mode === "block" && /\b(rm\s+-rf|sed\s+-i|git\s+checkout)\b/.test(String(event.input?.command ?? ""))) {
        return { block: true, reason: "agent-ledger blocks mutating bash in block mode (set AGENT_LEDGER_BASH_MODE=warn to allow)" };
      }
      // Fall through; record will scan after.
      return undefined;
    }

    return undefined;
  });

  pi.on("tool_result", async (event, _ctx) => {
    if (!bootstrapped) return;
    const toolName = (event.toolName || "").toLowerCase();

    // Edit tools: record against the intent claimed in tool_call.
    if (EDIT_TOOLS.has(toolName)) {
      const live = liveClaims.get(event.toolCallId);
      if (!live) return;
      liveClaims.delete(event.toolCallId);
      if (event.isError) return; // no record on error
      const summary = summaryFromInput(toolName, (event as any).input ?? {});
      await recordPaths(live.paths, live.intentId, summary);
      return;
    }

    // Bash: post-scan working tree, claim+record any newly modified paths.
    if (BASH_TOOLS.has(toolName) && !event.isError) {
      const dirty = await gitStatusPaths();
      if (dirty.length === 0) return;
      const claim = await claimPaths(dirty, `pi bash: ${String((event as any).input?.command ?? "").slice(0, 80)}`);
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
