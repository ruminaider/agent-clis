/**
 * End-to-end tests for the pi subagent child-bootstrap path using the
 * real agent-ledger binary.
 *
 * Each test creates an isolated temp directory and fresh ledger, runs
 * session-bootstrap.sh in subagent child mode against the real kernel,
 * then inspects the resulting ledger state with agent-ledger CLI
 * commands (assignments, verify, claim, and the raw audit JSONL).
 * Nine scenarios exercise the child self-assignment contract described
 * in tasks/option-d-context.md.
 *
 * The real binary is located from AGENT_LEDGER_BIN (set by run.sh
 * before invoking node tests). When running this file directly with
 * node --test, the fallback path is bin/agent-ledger under the repo
 * root. run.sh builds the binary once before invoking node tests.
 *
 * Run directly with:
 *   node --test adapters/tests/pi-subagent-e2e.test.mjs
 */

import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  writeFileSync,
  readFileSync,
  rmSync,
  existsSync,
  readdirSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = join(here, "../..");
const bootstrapSh = join(here, "../shared/session-bootstrap.sh");

// Locate the real agent-ledger binary. AGENT_LEDGER_BIN is exported by
// run.sh before it invokes node tests. When running the test file
// directly, fall back to the pre-built binary at bin/agent-ledger in
// the repo root. Build it on demand if neither path resolves.
function resolveRealBin() {
  if (process.env.AGENT_LEDGER_BIN) {
    const p = process.env.AGENT_LEDGER_BIN;
    if (existsSync(p)) return p;
  }
  const repoBin = join(repoRoot, "bin", "agent-ledger");
  if (existsSync(repoBin)) return repoBin;
  const r = spawnSync(
    "go",
    ["build", "-o", repoBin, "./cmd/agent-ledger"],
    { cwd: repoRoot, encoding: "utf8" }
  );
  if (r.status !== 0) {
    throw new Error(
      `Failed to build agent-ledger binary:\n${r.stderr || r.stdout}`
    );
  }
  return repoBin;
}

const REAL_BIN = resolveRealBin();
const REAL_BIN_DIR = dirname(REAL_BIN);

// Fixed test values. Constants make byte-exact assertions stable and
// readable. They correspond to the decision 5 and decision 6 id formats
// from tasks/option-d-context.md.
const PARENT_TASK = "parent/task";
const PARENT_AGENT = "agent:pi:parent:42";
const RUN_ID_A = "run-abc";
const RUN_ID_B = "run-def";
const CHILD_AGENT_NAME = "worker";

// Decision 5: child task id format.
//   <parent_task>/<child_agent>/<run_id>-<index>
function childTaskId(parentTask, childAgent, runId, index) {
  return `${parentTask}/${childAgent}/${runId}-${index}`;
}

// Decision 6: child AGENT_ID format.
//   agent:pi:subagent:<run_id>:<index>
function childAgentId(runId, index) {
  return `agent:pi:subagent:${runId}:${index}`;
}

// Concrete expected values for RUN_ID_A (run-abc).
const CHILD_TASK_0 = childTaskId(PARENT_TASK, CHILD_AGENT_NAME, RUN_ID_A, 0);
const CHILD_TASK_1 = childTaskId(PARENT_TASK, CHILD_AGENT_NAME, RUN_ID_A, 1);
const CHILD_TASK_2 = childTaskId(PARENT_TASK, CHILD_AGENT_NAME, RUN_ID_A, 2);
const CHILD_AGENT_ID_0 = childAgentId(RUN_ID_A, 0);
const CHILD_AGENT_ID_1 = childAgentId(RUN_ID_A, 1);
const CHILD_AGENT_ID_2 = childAgentId(RUN_ID_A, 2);

// Expected values for RUN_ID_B (run-def), same index 0.
const CHILD_TASK_RUNB_0 = childTaskId(PARENT_TASK, CHILD_AGENT_NAME, RUN_ID_B, 0);

// Complete subagent environment for the default child (run-abc, index 0).
const BASE_SUBAGENT_ENV = {
  PI_SUBAGENT_CHILD: "1",
  PI_SUBAGENT_RUN_ID: RUN_ID_A,
  PI_SUBAGENT_CHILD_INDEX: "0",
  PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT_NAME,
  AGENT_LEDGER_TASK_ID: PARENT_TASK,
  AGENT_ID: PARENT_AGENT,
};

// Build a clean subprocess environment. Not inheriting the outer process
// env avoids bleeding of subagent vars or ledger state from the shell
// running the tests. The real binary directory is prepended to PATH so
// session-bootstrap.sh finds both agent-ledger and system tools (git,
// python3, bash, etc.).
function makeEnv(ledgerDir, extra = {}) {
  return {
    PATH: `${REAL_BIN_DIR}:${process.env.PATH || "/usr/bin:/bin:/usr/local/bin"}`,
    HOME: process.env.HOME || "/tmp",
    USER: process.env.USER || "test",
    SHELL: "/bin/bash",
    TMPDIR: tmpdir(),
    LANG: "C",
    AGENT_LEDGER_DIR: ledgerDir,
    ...extra,
  };
}

// Create an isolated temp project directory and a fresh ledger inside
// it. Initializes the ledger with the real binary. Returns paths and a
// cleanup function the test should register with t.after().
function makeTestSetup() {
  const projDir = mkdtempSync(join(tmpdir(), "pi-e2e-proj-"));
  const ledgerDir = join(projDir, "ledger");
  mkdirSync(ledgerDir, { recursive: true });
  const env = makeEnv(ledgerDir);
  const r = spawnSync(REAL_BIN, ["init", "--ledger-dir", ledgerDir], {
    cwd: projDir,
    encoding: "utf8",
    env,
  });
  if (r.status !== 0) {
    throw new Error(`agent-ledger init failed:\n${r.stderr}`);
  }
  function cleanup() {
    try {
      rmSync(projDir, { recursive: true, force: true });
    } catch (_) {}
  }
  return { projDir, ledgerDir, cleanup };
}

// Run session-bootstrap.sh with the given environment and optional extra
// flags. Always includes --harness and --agent-kind.
function runBootstrap(env, extraFlags = []) {
  return spawnSync(
    "bash",
    [bootstrapSh, "--harness", "pi", "--agent-kind", "worker", ...extraFlags],
    { encoding: "utf8", env }
  );
}

// Parse the JSON payload from bootstrap --json output. The line format is:
//   AGENT_LEDGER_BOOTSTRAP_JSON={...}
function parseBootstrapJson(stdout) {
  const prefix = "AGENT_LEDGER_BOOTSTRAP_JSON=";
  const line = stdout
    .trim()
    .split("\n")
    .find((l) => l.startsWith(prefix));
  assert.ok(
    line,
    `no AGENT_LEDGER_BOOTSTRAP_JSON line in bootstrap output:\n${stdout}`
  );
  return JSON.parse(line.slice(prefix.length));
}

// Query assignments for a task and return the parsed JSON response.
function queryAssignments(ledgerDir, taskId) {
  const env = makeEnv(ledgerDir);
  const r = spawnSync(
    REAL_BIN,
    ["assignments", "--task", taskId, "--json", "--ledger-dir", ledgerDir],
    { encoding: "utf8", env }
  );
  assert.equal(r.status, 0, `assignments query failed:\n${r.stderr}`);
  return JSON.parse(r.stdout);
}

// Run verify scoped to a task and return the parsed JSON response.
// verify exits 1 when error-severity findings exist but still produces
// JSON on stdout; the caller checks the status field in the parsed output.
function queryVerify(projDir, ledgerDir, taskId) {
  const env = makeEnv(ledgerDir);
  const r = spawnSync(
    REAL_BIN,
    ["verify", "--task", taskId, "--json", "--ledger-dir", ledgerDir],
    { cwd: projDir, encoding: "utf8", env }
  );
  return JSON.parse(r.stdout);
}

// Read all audit events from the ledger's audit JSONL files and return
// them as an array of parsed objects.
function readAuditEvents(ledgerDir) {
  const auditDir = join(ledgerDir, "audit");
  let files = [];
  try {
    files = readdirSync(auditDir).filter((f) => f.endsWith(".jsonl"));
  } catch (_) {
    return [];
  }
  const events = [];
  for (const f of files) {
    const content = readFileSync(join(auditDir, f), "utf8");
    for (const line of content.split("\n")) {
      const trimmed = line.trim();
      if (!trimmed) continue;
      try {
        events.push(JSON.parse(trimmed));
      } catch (_) {}
    }
  }
  return events;
}

// Test 1: Single child, with metadata and clean verify.
test(
  "single child: assignment row written with correct metadata and verify passes",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    const env = makeEnv(ledgerDir, { ...BASE_SUBAGENT_ENV });
    const r = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r.status, 0, `bootstrap failed:\n${r.stderr}`);

    const json = parseBootstrapJson(r.stdout);
    // Decision 5: byte-exact child task id.
    assert.equal(json.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);
    // Decision 6: byte-exact child AGENT_ID.
    assert.equal(json.AGENT_ID, CHILD_AGENT_ID_0);
    assert.equal(json.AGENT_LEDGER_TASK_SOURCE, "subagent");
    assert.equal(json.AGENT_LEDGER_AUTO_ASSIGNED, "0");
    assert.equal(json.AGENT_LEDGER_PARENT_TASK_ID, PARENT_TASK);

    const assignments = queryAssignments(ledgerDir, CHILD_TASK_0);
    assert.equal(assignments.count, 1);
    const a = assignments.assignments[0];
    // Byte-exact key fields on the stored row.
    assert.equal(a.task_id, CHILD_TASK_0);
    assert.equal(a.assigned_agent, CHILD_AGENT_ID_0);
    assert.equal(a.orchestrator_id, PARENT_AGENT);
    // Decision 7: metadata schema.
    assert.equal(a.metadata.dispatch_origin, "pi-subagent-bootstrap");
    assert.equal(a.metadata.parent_task, PARENT_TASK);
    assert.equal(a.metadata.parent_agent_id, PARENT_AGENT);
    assert.equal(a.metadata.subagent_run_id, RUN_ID_A);
    assert.equal(a.metadata.subagent_child_index, 0);
    assert.equal(a.metadata.subagent_child_agent, CHILD_AGENT_NAME);

    // Verify must pass with no findings (no AUTO_ASSIGNED_TASK, no
    // AGENT_MISMATCH, no other findings).
    const verify = queryVerify(projDir, ledgerDir, CHILD_TASK_0);
    assert.equal(verify.status, "passed");
    assert.equal(
      verify.findings.length,
      0,
      `unexpected findings: ${JSON.stringify(verify.findings)}`
    );
  }
);

// Test 2: Parallel tasks:[...] siblings with the same child agent name
// and same task text (indices 0 and 1, same run id).
test(
  "parallel siblings: indices 0 and 1 produce distinct rows differing only in index suffix",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    // Child at index 0.
    const env0 = makeEnv(ledgerDir, {
      ...BASE_SUBAGENT_ENV,
      PI_SUBAGENT_CHILD_INDEX: "0",
    });
    const r0 = runBootstrap(env0, ["--json", "--cwd", projDir]);
    assert.equal(r0.status, 0, `bootstrap index=0 failed:\n${r0.stderr}`);

    // Child at index 1.
    const env1 = makeEnv(ledgerDir, {
      ...BASE_SUBAGENT_ENV,
      PI_SUBAGENT_CHILD_INDEX: "1",
    });
    const r1 = runBootstrap(env1, ["--json", "--cwd", projDir]);
    assert.equal(r1.status, 0, `bootstrap index=1 failed:\n${r1.stderr}`);

    const json0 = parseBootstrapJson(r0.stdout);
    const json1 = parseBootstrapJson(r1.stdout);

    // Byte-exact task ids: differ only in the index suffix.
    assert.equal(json0.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);
    assert.equal(json1.AGENT_LEDGER_TASK_ID, CHILD_TASK_1);
    // Byte-exact AGENT_IDs: differ only in the index suffix.
    assert.equal(json0.AGENT_ID, CHILD_AGENT_ID_0);
    assert.equal(json1.AGENT_ID, CHILD_AGENT_ID_1);

    // Two distinct assignment rows in the same ledger.
    const a0 = queryAssignments(ledgerDir, CHILD_TASK_0);
    const a1 = queryAssignments(ledgerDir, CHILD_TASK_1);
    assert.equal(a0.count, 1);
    assert.equal(a1.count, 1);
    assert.notEqual(
      a0.assignments[0].assignment_id,
      a1.assignments[0].assignment_id,
      "siblings must have distinct assignment IDs"
    );

    // Both pass verify cleanly.
    const v0 = queryVerify(projDir, ledgerDir, CHILD_TASK_0);
    const v1 = queryVerify(projDir, ledgerDir, CHILD_TASK_1);
    assert.equal(v0.status, "passed");
    assert.equal(v0.findings.length, 0);
    assert.equal(v1.status, "passed");
    assert.equal(v1.findings.length, 0);
  }
);

// Test 3: Two separate subagent() calls in one turn, both at child index 0
// but with different run ids.
test(
  "two calls at index 0 with different run ids produce distinct task ids",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    // First call: run-abc, index 0.
    const envA = makeEnv(ledgerDir, {
      ...BASE_SUBAGENT_ENV,
      PI_SUBAGENT_RUN_ID: RUN_ID_A,
      PI_SUBAGENT_CHILD_INDEX: "0",
    });
    const rA = runBootstrap(envA, ["--json", "--cwd", projDir]);
    assert.equal(rA.status, 0, `bootstrap run-a failed:\n${rA.stderr}`);

    // Second call: run-def, index 0.
    const envB = makeEnv(ledgerDir, {
      ...BASE_SUBAGENT_ENV,
      PI_SUBAGENT_RUN_ID: RUN_ID_B,
      PI_SUBAGENT_CHILD_INDEX: "0",
    });
    const rB = runBootstrap(envB, ["--json", "--cwd", projDir]);
    assert.equal(rB.status, 0, `bootstrap run-b failed:\n${rB.stderr}`);

    const jsonA = parseBootstrapJson(rA.stdout);
    const jsonB = parseBootstrapJson(rB.stdout);

    // Byte-exact: task ids differ in the run-id portion.
    assert.equal(jsonA.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);       // run-abc-0
    assert.equal(jsonB.AGENT_LEDGER_TASK_ID, CHILD_TASK_RUNB_0);  // run-def-0
    assert.notEqual(
      jsonA.AGENT_LEDGER_TASK_ID,
      jsonB.AGENT_LEDGER_TASK_ID
    );

    // Two distinct assignment rows in the same ledger.
    const aA = queryAssignments(ledgerDir, CHILD_TASK_0);
    const aB = queryAssignments(ledgerDir, CHILD_TASK_RUNB_0);
    assert.equal(aA.count, 1);
    assert.equal(aB.count, 1);

    // Both pass verify cleanly.
    const vA = queryVerify(projDir, ledgerDir, CHILD_TASK_0);
    const vB = queryVerify(projDir, ledgerDir, CHILD_TASK_RUNB_0);
    assert.equal(vA.status, "passed");
    assert.equal(vA.findings.length, 0);
    assert.equal(vB.status, "passed");
    assert.equal(vB.findings.length, 0);
  }
);

// Test 4: count:N expansion with unique deterministic child task ids
// (three children at indices 0, 1, 2, same run id).
test(
  "count:N expansion: three children at indices 0, 1, 2 produce three distinct rows",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    const expectedTaskIds = [CHILD_TASK_0, CHILD_TASK_1, CHILD_TASK_2];
    const expectedAgentIds = [CHILD_AGENT_ID_0, CHILD_AGENT_ID_1, CHILD_AGENT_ID_2];

    for (const index of [0, 1, 2]) {
      const env = makeEnv(ledgerDir, {
        ...BASE_SUBAGENT_ENV,
        PI_SUBAGENT_CHILD_INDEX: String(index),
      });
      const r = runBootstrap(env, ["--json", "--cwd", projDir]);
      assert.equal(
        r.status,
        0,
        `bootstrap index=${index} failed:\n${r.stderr}`
      );
      const json = parseBootstrapJson(r.stdout);
      // Byte-exact task id and AGENT_ID for each index.
      assert.equal(
        json.AGENT_LEDGER_TASK_ID,
        expectedTaskIds[index],
        `index=${index} task id`
      );
      assert.equal(
        json.AGENT_ID,
        expectedAgentIds[index],
        `index=${index} AGENT_ID`
      );
    }

    // Three distinct assignment rows in the ledger.
    for (const taskId of expectedTaskIds) {
      const assignments = queryAssignments(ledgerDir, taskId);
      assert.equal(
        assignments.count,
        1,
        `expected one row for ${taskId}, got ${assignments.count}`
      );
      assert.equal(assignments.assignments[0].task_id, taskId);
    }
  }
);

// Test 5: Async/background dispatch self-assigns correctly (not branch or
// auto fallback). Confirms that TASK_SOURCE=subagent is used even when
// the project directory is a git repo with a detectable branch.
test(
  "async dispatch: TASK_SOURCE=subagent wins over git branch detection",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    // Set up a git repo so branch detection would normally fire.
    spawnSync("git", ["-C", projDir, "init", "-q"], { encoding: "utf8" });
    spawnSync(
      "git",
      [
        "-C", projDir,
        "-c", "user.email=t@t",
        "-c", "user.name=t",
        "commit", "--allow-empty", "-qm", "init",
      ],
      { encoding: "utf8" }
    );
    spawnSync(
      "git",
      ["-C", projDir, "checkout", "-q", "-b", "feature/some-branch"],
      { encoding: "utf8" }
    );

    const env = makeEnv(ledgerDir, { ...BASE_SUBAGENT_ENV });
    const r = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r.status, 0, `bootstrap failed:\n${r.stderr}`);

    const json = parseBootstrapJson(r.stdout);
    // Subagent path wins: TASK_SOURCE must be "subagent", not "branch".
    assert.equal(json.AGENT_LEDGER_TASK_SOURCE, "subagent");
    // Task id is the deterministic child id, not the git branch name.
    assert.equal(json.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);
  }
);

// Test 6: Retry or respawn with deterministic reuse. Running bootstrap
// twice with identical env produces one assignment row (--if-absent reuse).
test(
  "retry or respawn: two identical bootstrap invocations produce one assignment row",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    const env = makeEnv(ledgerDir, { ...BASE_SUBAGENT_ENV });

    // First invocation.
    const r1 = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r1.status, 0, `first bootstrap failed:\n${r1.stderr}`);
    const json1 = parseBootstrapJson(r1.stdout);

    // Second invocation with identical env (simulates retry or respawn).
    const r2 = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r2.status, 0, `second bootstrap failed:\n${r2.stderr}`);
    const json2 = parseBootstrapJson(r2.stdout);

    // Byte-exact: same task id and AGENT_ID across both invocations.
    assert.equal(json1.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);
    assert.equal(json2.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);
    assert.equal(json1.AGENT_ID, CHILD_AGENT_ID_0);
    assert.equal(json2.AGENT_ID, CHILD_AGENT_ID_0);

    // Exactly one assignment row in the ledger (--if-absent reuse path).
    const assignments = queryAssignments(ledgerDir, CHILD_TASK_0);
    assert.equal(
      assignments.count,
      1,
      `expected count=1 after two bootstrap invocations (--if-absent reuse), got ${assignments.count}`
    );

    // Both verify calls pass cleanly.
    const v1 = queryVerify(projDir, ledgerDir, CHILD_TASK_0);
    const v2 = queryVerify(projDir, ledgerDir, CHILD_TASK_0);
    assert.equal(v1.status, "passed");
    assert.equal(v1.findings.length, 0);
    assert.equal(v2.status, "passed");
    assert.equal(v2.findings.length, 0);
  }
);

// Test 7: Audit ordering. The task.assigned event (written by bootstrap)
// must have a timestamp strictly before the first intent.opened event
// (written by a subsequent claim call).
test(
  "audit ordering: task.assigned timestamp precedes intent.opened timestamp",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    // Bootstrap writes the task.assigned event.
    const env = makeEnv(ledgerDir, { ...BASE_SUBAGENT_ENV });
    const r = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r.status, 0, `bootstrap failed:\n${r.stderr}`);

    // Create a file, then claim it to produce an intent.opened event.
    const filePath = join(projDir, "work.txt");
    writeFileSync(filePath, "placeholder content");
    const claimEnv = makeEnv(ledgerDir);
    const claimResult = spawnSync(
      REAL_BIN,
      [
        "claim",
        filePath,
        "--task", CHILD_TASK_0,
        "--reason", "test claim for audit ordering",
        "--agent", CHILD_AGENT_ID_0,
        "--ledger-dir", ledgerDir,
      ],
      { cwd: projDir, encoding: "utf8", env: claimEnv }
    );
    assert.equal(
      claimResult.status,
      0,
      `claim failed:\n${claimResult.stderr}`
    );

    // Read all audit events from the ledger.
    const events = readAuditEvents(ledgerDir);

    const assignedEvent = events.find(
      (e) =>
        e.event_type === "task.assigned" && e.task_id === CHILD_TASK_0
    );
    const openedEvent = events.find(
      (e) =>
        e.event_type === "intent.opened" && e.task_id === CHILD_TASK_0
    );

    assert.ok(
      assignedEvent,
      `no task.assigned event found for ${CHILD_TASK_0} in audit log`
    );
    assert.ok(
      openedEvent,
      `no intent.opened event found for ${CHILD_TASK_0} in audit log`
    );

    const assignedTime = Date.parse(assignedEvent.created_at);
    const openedTime = Date.parse(openedEvent.created_at);

    assert.ok(
      assignedTime < openedTime,
      `expected task.assigned (${assignedEvent.created_at}) to be strictly` +
        ` before intent.opened (${openedEvent.created_at})`
    );
  }
);

// Test 8: Cross-repo cwd. A child running in child-repo writes its
// assignment to child-repo's ledger (via AGENT_LEDGER_DIR). The parent
// ledger is not written. metadata.parent_task records the informational
// cross-ledger linkage.
test(
  "cross-repo cwd: child assignment goes to child ledger, not parent ledger",
  (t) => {
    const parentDir = mkdtempSync(join(tmpdir(), "pi-e2e-parent-"));
    const childDir = mkdtempSync(join(tmpdir(), "pi-e2e-child-"));
    const parentLedgerDir = join(parentDir, "ledger");
    const childLedgerDir = join(childDir, "ledger");
    mkdirSync(parentLedgerDir, { recursive: true });
    mkdirSync(childLedgerDir, { recursive: true });

    t.after(() => {
      try { rmSync(parentDir, { recursive: true, force: true }); } catch (_) {}
      try { rmSync(childDir, { recursive: true, force: true }); } catch (_) {}
    });

    // Initialize both ledgers independently.
    const parentInitResult = spawnSync(
      REAL_BIN,
      ["init", "--ledger-dir", parentLedgerDir],
      { cwd: parentDir, encoding: "utf8", env: makeEnv(parentLedgerDir) }
    );
    assert.equal(
      parentInitResult.status,
      0,
      `parent ledger init failed:\n${parentInitResult.stderr}`
    );

    const childInitResult = spawnSync(
      REAL_BIN,
      ["init", "--ledger-dir", childLedgerDir],
      { cwd: childDir, encoding: "utf8", env: makeEnv(childLedgerDir) }
    );
    assert.equal(
      childInitResult.status,
      0,
      `child ledger init failed:\n${childInitResult.stderr}`
    );

    // Bootstrap from child-repo context. AGENT_LEDGER_DIR points to the
    // child ledger. The parent task id in AGENT_LEDGER_TASK_ID is purely
    // informational; the child writes its own assignment into the child
    // ledger only.
    const childEnv = makeEnv(childLedgerDir, { ...BASE_SUBAGENT_ENV });
    const r = runBootstrap(childEnv, ["--json", "--cwd", childDir]);
    assert.equal(r.status, 0, `bootstrap failed:\n${r.stderr}`);

    const json = parseBootstrapJson(r.stdout);
    assert.equal(json.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);

    // Child assignment exists in the child ledger.
    const childAssignments = queryAssignments(childLedgerDir, CHILD_TASK_0);
    assert.equal(
      childAssignments.count,
      1,
      "child assignment must exist in child ledger"
    );
    // metadata.parent_task records the informational cross-ledger linkage.
    assert.equal(
      childAssignments.assignments[0].metadata.parent_task,
      PARENT_TASK
    );

    // Parent ledger must not contain the child assignment row.
    const parentAssignments = queryAssignments(parentLedgerDir, CHILD_TASK_0);
    assert.equal(
      parentAssignments.count,
      0,
      "child assignment must not appear in parent ledger"
    );
  }
);

// Test 9: Zero-tool child. Bootstrap is eager: the assignment row exists
// immediately after bootstrap without any subsequent claim, record, or
// heartbeat call. Verifies decision 2 (eager bootstrap chronology).
test(
  "zero-tool child: assignment row exists before any tool calls",
  (t) => {
    const { projDir, ledgerDir, cleanup } = makeTestSetup();
    t.after(cleanup);

    // Bootstrap only; no claim/record/heartbeat follows.
    const env = makeEnv(ledgerDir, { ...BASE_SUBAGENT_ENV });
    const r = runBootstrap(env, ["--json", "--cwd", projDir]);
    assert.equal(r.status, 0, `bootstrap failed:\n${r.stderr}`);

    const json = parseBootstrapJson(r.stdout);
    assert.equal(json.AGENT_LEDGER_TASK_ID, CHILD_TASK_0);

    // Assignment row must exist immediately (eager bootstrap, no tool call
    // needed to trigger assignment).
    const assignments = queryAssignments(ledgerDir, CHILD_TASK_0);
    assert.equal(
      assignments.count,
      1,
      "assignment row must exist even when the child issues no tool calls"
    );
    assert.equal(assignments.assignments[0].task_id, CHILD_TASK_0);
  }
);
