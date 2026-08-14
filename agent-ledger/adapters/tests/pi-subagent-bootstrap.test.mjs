/**
 * Structured tests for session-bootstrap.sh in subagent child mode.
 *
 * Each test exercises the bootstrap under a synthetic subagent environment
 * using an in-process agent-ledger stub. The assertions validate:
 *   - Decision 5: child task id format
 *     `<parent_task>/<child_agent>/<run_id>-<child_index>`
 *   - Decision 6: child AGENT_ID format
 *     `agent:pi:subagent:<run_id>:<child_index>`
 *   - Linked and orphan metadata schemas, including
 *     subagent_child_index as a JSON number
 *
 * Run directly with:
 *   node --test adapters/tests/pi-subagent-bootstrap.test.mjs
 */

import test from "node:test";
import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  mkdirSync,
  mkdtempSync,
  writeFileSync,
  chmodSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const bootstrapSh = join(here, "../shared/session-bootstrap.sh");

// Stub script for agent-ledger. Reads AGENT_LEDGER_STUB_LOG from env
// to know where to write the invocation log. Reads
// AGENT_LEDGER_STUB_METADATA_LOG when set to capture the --metadata value.
// Mirrors the stub used in run.sh so behavior is consistent.
const STUB_SCRIPT = `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$AGENT_LEDGER_STUB_LOG"
case "$1" in
  identify) exit 0 ;;
  assignments)
    count="\${AGENT_LEDGER_STUB_ASSIGNMENTS_COUNT:-0}"
    printf '{"assignments":[],"count":%s,"schema":"agent-ledger.assignments.v1"}\\n' "$count"
    exit 0 ;;
  assign)
    if [[ "\${2:-}" == "--help" ]]; then
      printf 'usage: agent-ledger assign [--task] [--reason] [--metadata]\\n'
      exit 0
    fi
    if [[ -n "\${AGENT_LEDGER_STUB_METADATA_LOG:-}" ]]; then
      while [[ $# -gt 0 ]]; do
        case "$1" in
          --metadata)
            printf '%s\\n' "$2" > "$AGENT_LEDGER_STUB_METADATA_LOG"
            shift 2
            ;;
          *) shift ;;
        esac
      done
    fi
    exit 0 ;;
esac
`;

/**
 * Create an isolated temp directory with the agent-ledger stub installed.
 * Returns paths and a cleanup function that the test should register with
 * t.after().
 */
function makeTestEnv() {
  const dir = mkdtempSync(join(tmpdir(), "pi-bootstrap-test-"));
  const binDir = join(dir, "bin");
  const stubLog = join(dir, "ledger.log");
  mkdirSync(binDir, { recursive: true });
  writeFileSync(join(binDir, "agent-ledger"), STUB_SCRIPT);
  chmodSync(join(binDir, "agent-ledger"), 0o755);
  writeFileSync(stubLog, "");
  function cleanup() {
    try {
      rmSync(dir, { recursive: true, force: true });
    } catch (_) {}
  }
  return { dir, binDir, stubLog, cleanup };
}

/**
 * Build a minimal environment for a bootstrap invocation. Never inherits
 * PI_SUBAGENT_CHILD or other subagent vars from the outer process so tests
 * do not bleed into each other.
 */
function makeEnv(binDir, stubLog, extra = {}) {
  return {
    PATH: `${binDir}:${process.env.PATH || "/usr/bin:/bin"}`,
    HOME: process.env.HOME || "/tmp",
    USER: process.env.USER || "test",
    SHELL: "/bin/bash",
    TMPDIR: tmpdir(),
    AGENT_LEDGER_STUB_LOG: stubLog,
    ...extra,
  };
}

/**
 * Run session-bootstrap.sh with the given environment and extra flags.
 * Always passes --harness, --agent-kind, and --cwd for a complete
 * invocation (matching the pattern used in run.sh).
 */
function runBootstrap(env, extraFlags = []) {
  const flags = [
    "--harness", "pi",
    "--agent-kind", "worker",
    "--cwd", tmpdir(),
    ...extraFlags,
  ];
  return spawnSync("bash", [bootstrapSh, ...flags], {
    encoding: "utf8",
    env,
  });
}

/**
 * Parse the JSON payload from bootstrap --json output.
 * The output line has the form:
 *   AGENT_LEDGER_BOOTSTRAP_JSON={...}
 */
function parseBootstrapOutput(stdout) {
  const prefix = "AGENT_LEDGER_BOOTSTRAP_JSON=";
  const line = stdout
    .trim()
    .split("\n")
    .find((l) => l.startsWith(prefix));
  assert.ok(line, `no AGENT_LEDGER_BOOTSTRAP_JSON line in output:\n${stdout}`);
  return JSON.parse(line.slice(prefix.length));
}

// Known test inputs. Fixed values make assertions byte-exact.
const PARENT_TASK = "parent/task";
const PARENT_AGENT = "agent:pi:parent:42";
const RUN_ID = "run-abc";
const CHILD_AGENT = "worker";

// Expected derived values for child index 0 (decisions 5 and 6).
const CHILD_TASK_INDEX_0 = `${PARENT_TASK}/${CHILD_AGENT}/${RUN_ID}-0`;
const CHILD_AGENT_ID_INDEX_0 = `agent:pi:subagent:${RUN_ID}:0`;

// Expected derived values for child index 1.
const CHILD_TASK_INDEX_1 = `${PARENT_TASK}/${CHILD_AGENT}/${RUN_ID}-1`;
const CHILD_AGENT_ID_INDEX_1 = `agent:pi:subagent:${RUN_ID}:1`;
const ORPHAN_TASK_INDEX_0 = `auto/pi-subagent/${RUN_ID}-0`;

// Complete subagent env for child index 0.
const FULL_SUBAGENT_ENV = {
  PI_SUBAGENT_CHILD: "1",
  PI_SUBAGENT_RUN_ID: RUN_ID,
  PI_SUBAGENT_CHILD_INDEX: "0",
  PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
  AGENT_LEDGER_TASK_ID: PARENT_TASK,
  AGENT_ID: PARENT_AGENT,
};

const ORPHAN_SUBAGENT_ENV = {
  PI_SUBAGENT_CHILD: "1",
  PI_SUBAGENT_RUN_ID: RUN_ID,
  PI_SUBAGENT_CHILD_INDEX: "0",
  PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
};

// Test 1: happy path.
test(
  "happy path: full child env produces deterministic ids and metadata",
  (t) => {
    const { dir, binDir, stubLog, cleanup } = makeTestEnv();
    t.after(cleanup);
    const metaLog = join(dir, "meta.json");

    const env = makeEnv(binDir, stubLog, {
      ...FULL_SUBAGENT_ENV,
      AGENT_LEDGER_STUB_METADATA_LOG: metaLog,
    });
    const result = runBootstrap(env, ["--json"]);
    assert.equal(result.status, 0, `bootstrap failed:\n${result.stderr}`);

    const output = parseBootstrapOutput(result.stdout);

    // Decision 5: child task id.
    assert.equal(
      output.AGENT_LEDGER_TASK_ID,
      CHILD_TASK_INDEX_0,
      "decision 5: child task id must match <parent>/<agent>/<run_id>-<index>"
    );

    // Decision 6: child AGENT_ID.
    assert.equal(
      output.AGENT_ID,
      CHILD_AGENT_ID_INDEX_0,
      "decision 6: child AGENT_ID must match agent:pi:subagent:<run_id>:<index>"
    );

    assert.equal(output.AGENT_LEDGER_TASK_SOURCE, "subagent");
    assert.equal(output.AGENT_LEDGER_AUTO_ASSIGNED, "0");

    const log = readFileSync(stubLog, "utf8");

    // Parent AGENT_ID is passed as --orchestrator (not as child identity).
    assert.ok(
      log.includes(`--orchestrator ${PARENT_AGENT}`),
      `stub log must contain --orchestrator ${PARENT_AGENT}:\n${log}`
    );

    // Derived child AGENT_ID is passed as --agent.
    assert.ok(
      log.includes(`--agent ${CHILD_AGENT_ID_INDEX_0}`),
      `stub log must contain --agent ${CHILD_AGENT_ID_INDEX_0}:\n${log}`
    );

    // Orchestrator and agent must be distinct identities.
    assert.notEqual(
      PARENT_AGENT,
      CHILD_AGENT_ID_INDEX_0,
      "parent and child AGENT_IDs must be distinct"
    );

    assert.ok(
      log.includes("--if-absent"),
      `stub log must contain --if-absent:\n${log}`
    );

    // Decision 7: metadata schema.
    const rawMeta = readFileSync(metaLog, "utf8").trim();
    const meta = JSON.parse(rawMeta);
    assert.equal(meta.parent_task, PARENT_TASK, "decision 7: parent_task");
    assert.equal(meta.parent_agent_id, PARENT_AGENT, "decision 7: parent_agent_id");
    assert.equal(meta.subagent_run_id, RUN_ID, "decision 7: subagent_run_id");
    assert.equal(meta.subagent_child_index, 0, "decision 7: subagent_child_index value");
    assert.equal(
      typeof meta.subagent_child_index,
      "number",
      "decision 7: subagent_child_index must be a JSON number, not a string"
    );
    assert.equal(meta.subagent_child_agent, CHILD_AGENT, "decision 7: subagent_child_agent");
    assert.equal(
      meta.dispatch_origin,
      "pi-subagent-bootstrap",
      "decision 7: dispatch_origin discriminator"
    );
  }
);

test("orphan parent context: deterministic auto-assignment preserves review visibility", (t) => {
  const { dir, binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const metaLog = join(dir, "orphan-meta.json");
  const env = makeEnv(binDir, stubLog, {
    ...ORPHAN_SUBAGENT_ENV,
    PI_SESSION_ID: "orphan-session-123",
    AGENT_LEDGER_STUB_METADATA_LOG: metaLog,
  });

  const first = runBootstrap(env, ["--orchestrator", "pi-extension", "--json"]);
  assert.equal(first.status, 0, `first orphan bootstrap failed:\n${first.stderr}`);
  const second = runBootstrap(env, ["--orchestrator", "pi-extension", "--json"]);
  assert.equal(second.status, 0, `second orphan bootstrap failed:\n${second.stderr}`);
  const firstOutput = parseBootstrapOutput(first.stdout);
  const secondOutput = parseBootstrapOutput(second.stdout);

  assert.equal(firstOutput.AGENT_LEDGER_TASK_ID, ORPHAN_TASK_INDEX_0);
  assert.equal(secondOutput.AGENT_LEDGER_TASK_ID, ORPHAN_TASK_INDEX_0);
  assert.equal(firstOutput.AGENT_ID, CHILD_AGENT_ID_INDEX_0);
  assert.equal(secondOutput.AGENT_ID, CHILD_AGENT_ID_INDEX_0);
  assert.equal(firstOutput.AGENT_LEDGER_TASK_SOURCE, "subagent-orphan");
  assert.equal(firstOutput.AGENT_LEDGER_AUTO_ASSIGNED, "1");
  assert.equal("AGENT_LEDGER_PARENT_TASK_ID" in firstOutput, false);

  const warnings = first.stderr
    .split("\n")
    .filter((line) => line.includes("WARNING"));
  assert.equal(warnings.length, 1, `expected one orphan warning:\n${first.stderr}`);
  assert.match(warnings[0], /AGENT_LEDGER_TASK_ID AGENT_ID/);
  assert.match(warnings[0], new RegExp(ORPHAN_TASK_INDEX_0));

  const log = readFileSync(stubLog, "utf8");
  assert.ok(log.includes("--orchestrator pi-extension"), `orphan uses adapter actor:\n${log}`);
  assert.ok(log.includes("[auto-assigned by pi-adapter auto-derived"), `orphan uses auto marker:\n${log}`);

  const meta = JSON.parse(readFileSync(metaLog, "utf8"));
  assert.equal(meta.auto_assigned, true);
  assert.equal(meta.task_source, "subagent-orphan");
  assert.equal(meta.dispatch_origin, "pi-subagent-orphan-bootstrap");
  assert.equal(meta.parent_context_missing, true);
  assert.deepEqual(meta.missing_parent_env, ["AGENT_LEDGER_TASK_ID", "AGENT_ID"]);
  assert.equal(meta.subagent_run_id, RUN_ID);
  assert.equal(meta.subagent_child_index, 0);
  assert.equal(typeof meta.subagent_child_index, "number");
  assert.equal(meta.subagent_child_agent, CHILD_AGENT);
  assert.equal(meta.pi_session_id, "orphan-session-123");
  assert.equal("parent_task" in meta, false);
  assert.equal("parent_agent_id" in meta, false);
});

test("orphan parent context: strict mode refuses auto-assignment", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const result = runBootstrap(makeEnv(binDir, stubLog, {
    ...ORPHAN_SUBAGENT_ENV,
    AGENT_LEDGER_REQUIRE_TASK: "1",
  }), ["--orchestrator", "pi-extension", "--json"]);

  assert.notEqual(result.status, 0, "strict mode must reject orphan auto-assignment");
  assert.match(result.stderr, /AGENT_LEDGER_REQUIRE_TASK=1/);
  assert.equal(readFileSync(stubLog, "utf8").includes("assign "), false);
});

// Rank guard: a fully linked child is resolved before the strict-mode
// check is consulted, so AGENT_LEDGER_REQUIRE_TASK=1 must not refuse a
// child that already has complete inherited parent context. Inverting
// those two ranks would fail this test.
test("linked parent context: strict mode still allows the linked source", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const result = runBootstrap(makeEnv(binDir, stubLog, {
    ...FULL_SUBAGENT_ENV,
    AGENT_LEDGER_REQUIRE_TASK: "1",
  }), ["--orchestrator", "pi-extension", "--json"]);

  assert.equal(result.status, 0, `strict mode must not refuse a linked child:\n${result.stderr}`);
  const output = parseBootstrapOutput(result.stdout);
  assert.equal(output.AGENT_LEDGER_TASK_SOURCE, "subagent");
  assert.equal(output.AGENT_LEDGER_TASK_ID, CHILD_TASK_INDEX_0);
  assert.equal(output.AGENT_LEDGER_AUTO_ASSIGNED, "0");
  assert.equal(output.AGENT_LEDGER_PARENT_TASK_ID, PARENT_TASK);
});

test("hard-fail: non-numeric PI_SUBAGENT_CHILD_INDEX", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const result = runBootstrap(makeEnv(binDir, stubLog, {
    ...ORPHAN_SUBAGENT_ENV,
    PI_SUBAGENT_CHILD_INDEX: "not-a-number",
  }), ["--json"]);

  assert.notEqual(result.status, 0, "expected non-zero exit for a non-numeric child index");
  assert.match(result.stderr, /PI_SUBAGENT_CHILD_INDEX must be a non-negative decimal integer/);
  assert.equal(readFileSync(stubLog, "utf8").includes("assign "), false);
});

// Test 2: hard-fail when PI_SUBAGENT_RUN_ID is missing.
test("hard-fail: missing PI_SUBAGENT_RUN_ID", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const env = makeEnv(binDir, stubLog, {
    PI_SUBAGENT_CHILD: "1",
    // PI_SUBAGENT_RUN_ID intentionally omitted.
    PI_SUBAGENT_CHILD_INDEX: "0",
    PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
    AGENT_LEDGER_TASK_ID: PARENT_TASK,
    AGENT_ID: PARENT_AGENT,
  });
  const result = runBootstrap(env, ["--json"]);
  assert.notEqual(
    result.status,
    0,
    "expected non-zero exit when PI_SUBAGENT_RUN_ID is missing"
  );
  assert.ok(
    result.stderr.includes("PI_SUBAGENT_RUN_ID") ||
      result.stderr.toLowerCase().includes("subagent"),
    `stderr must name PI_SUBAGENT_RUN_ID or the subagent contract:\n${result.stderr}`
  );
});

// Test 3: hard-fail when PI_SUBAGENT_CHILD_INDEX is missing.
test("hard-fail: missing PI_SUBAGENT_CHILD_INDEX", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const env = makeEnv(binDir, stubLog, {
    PI_SUBAGENT_CHILD: "1",
    PI_SUBAGENT_RUN_ID: RUN_ID,
    // PI_SUBAGENT_CHILD_INDEX intentionally omitted.
    PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
    AGENT_LEDGER_TASK_ID: PARENT_TASK,
    AGENT_ID: PARENT_AGENT,
  });
  const result = runBootstrap(env, ["--json"]);
  assert.notEqual(
    result.status,
    0,
    "expected non-zero exit when PI_SUBAGENT_CHILD_INDEX is missing"
  );
  assert.ok(
    result.stderr.includes("PI_SUBAGENT_CHILD_INDEX") ||
      result.stderr.toLowerCase().includes("subagent"),
    `stderr must name PI_SUBAGENT_CHILD_INDEX or the subagent contract:\n${result.stderr}`
  );
});

// Test 4: hard-fail when PI_SUBAGENT_CHILD_AGENT is missing.
test("hard-fail: missing PI_SUBAGENT_CHILD_AGENT", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const env = makeEnv(binDir, stubLog, {
    PI_SUBAGENT_CHILD: "1",
    PI_SUBAGENT_RUN_ID: RUN_ID,
    PI_SUBAGENT_CHILD_INDEX: "0",
    // PI_SUBAGENT_CHILD_AGENT intentionally omitted.
    AGENT_LEDGER_TASK_ID: PARENT_TASK,
    AGENT_ID: PARENT_AGENT,
  });
  const result = runBootstrap(env, ["--json"]);
  assert.notEqual(
    result.status,
    0,
    "expected non-zero exit when PI_SUBAGENT_CHILD_AGENT is missing"
  );
  assert.ok(
    result.stderr.includes("PI_SUBAGENT_CHILD_AGENT") ||
      result.stderr.toLowerCase().includes("subagent"),
    `stderr must name PI_SUBAGENT_CHILD_AGENT or the subagent contract:\n${result.stderr}`
  );
});

// Test 5: hard-fail when AGENT_LEDGER_TASK_ID (the inherited parent task) is missing.
test("hard-fail: missing AGENT_LEDGER_TASK_ID (parent task)", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const env = makeEnv(binDir, stubLog, {
    PI_SUBAGENT_CHILD: "1",
    PI_SUBAGENT_RUN_ID: RUN_ID,
    PI_SUBAGENT_CHILD_INDEX: "0",
    PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
    // AGENT_LEDGER_TASK_ID intentionally omitted.
    AGENT_ID: PARENT_AGENT,
  });
  const result = runBootstrap(env, ["--json"]);
  assert.notEqual(
    result.status,
    0,
    "expected non-zero exit when AGENT_LEDGER_TASK_ID is missing"
  );
  assert.ok(
    result.stderr.includes("AGENT_LEDGER_TASK_ID") ||
      result.stderr.toLowerCase().includes("subagent"),
    `stderr must name AGENT_LEDGER_TASK_ID or the subagent contract:\n${result.stderr}`
  );
});

// Test 6: hard-fail when AGENT_ID (the inherited parent agent id) is missing.
test("hard-fail: missing AGENT_ID (inherited parent agent id)", (t) => {
  const { binDir, stubLog, cleanup } = makeTestEnv();
  t.after(cleanup);
  const env = makeEnv(binDir, stubLog, {
    PI_SUBAGENT_CHILD: "1",
    PI_SUBAGENT_RUN_ID: RUN_ID,
    PI_SUBAGENT_CHILD_INDEX: "0",
    PI_SUBAGENT_CHILD_AGENT: CHILD_AGENT,
    AGENT_LEDGER_TASK_ID: PARENT_TASK,
    // AGENT_ID intentionally omitted.
  });
  const result = runBootstrap(env, ["--json"]);
  assert.notEqual(
    result.status,
    0,
    "expected non-zero exit when AGENT_ID is missing"
  );
  assert.ok(
    result.stderr.includes("AGENT_ID") ||
      result.stderr.toLowerCase().includes("subagent"),
    `stderr must name AGENT_ID or the subagent contract:\n${result.stderr}`
  );
});

// Test 7: same env produces byte-identical ids across two invocations.
test(
  "determinism: same env produces byte-identical ids across two invocations",
  (t) => {
    const { binDir, stubLog, cleanup } = makeTestEnv();
    t.after(cleanup);
    const env = makeEnv(binDir, stubLog, FULL_SUBAGENT_ENV);

    const first = runBootstrap(env, ["--json"]);
    assert.equal(first.status, 0, `first run failed:\n${first.stderr}`);
    const out1 = parseBootstrapOutput(first.stdout);

    const second = runBootstrap(env, ["--json"]);
    assert.equal(second.status, 0, `second run failed:\n${second.stderr}`);
    const out2 = parseBootstrapOutput(second.stdout);

    assert.equal(
      out1.AGENT_LEDGER_TASK_ID,
      out2.AGENT_LEDGER_TASK_ID,
      "task id must be byte-identical across invocations with the same env"
    );
    assert.equal(
      out1.AGENT_ID,
      out2.AGENT_ID,
      "AGENT_ID must be byte-identical across invocations with the same env"
    );

    // Sanity-check the exact expected values (decisions 5 and 6).
    assert.equal(out1.AGENT_LEDGER_TASK_ID, CHILD_TASK_INDEX_0);
    assert.equal(out1.AGENT_ID, CHILD_AGENT_ID_INDEX_0);
  }
);

// Test 8: distinct child indices produce distinct task ids and AGENT_IDs.
test(
  "distinct child indices produce distinct task ids and AGENT_IDs",
  (t) => {
    const { binDir, stubLog, cleanup } = makeTestEnv();
    t.after(cleanup);

    const env0 = makeEnv(binDir, stubLog, {
      ...FULL_SUBAGENT_ENV,
      PI_SUBAGENT_CHILD_INDEX: "0",
    });
    const env1 = makeEnv(binDir, stubLog, {
      ...FULL_SUBAGENT_ENV,
      PI_SUBAGENT_CHILD_INDEX: "1",
    });

    const res0 = runBootstrap(env0, ["--json"]);
    assert.equal(res0.status, 0, `index=0 run failed:\n${res0.stderr}`);
    const out0 = parseBootstrapOutput(res0.stdout);

    const res1 = runBootstrap(env1, ["--json"]);
    assert.equal(res1.status, 0, `index=1 run failed:\n${res1.stderr}`);
    const out1 = parseBootstrapOutput(res1.stdout);

    // Byte-exact values for each index.
    assert.equal(out0.AGENT_LEDGER_TASK_ID, CHILD_TASK_INDEX_0, "index=0 task id");
    assert.equal(out0.AGENT_ID, CHILD_AGENT_ID_INDEX_0, "index=0 AGENT_ID");
    assert.equal(out1.AGENT_LEDGER_TASK_ID, CHILD_TASK_INDEX_1, "index=1 task id");
    assert.equal(out1.AGENT_ID, CHILD_AGENT_ID_INDEX_1, "index=1 AGENT_ID");

    // The two outputs must differ.
    assert.notEqual(
      out0.AGENT_LEDGER_TASK_ID,
      out1.AGENT_LEDGER_TASK_ID,
      "task ids must differ when child index differs"
    );
    assert.notEqual(
      out0.AGENT_ID,
      out1.AGENT_ID,
      "AGENT_IDs must differ when child index differs"
    );

    // Verify that only the index suffix changes.
    assert.ok(
      out0.AGENT_LEDGER_TASK_ID.endsWith("-0"),
      `index=0 task id must end with -0: ${out0.AGENT_LEDGER_TASK_ID}`
    );
    assert.ok(
      out1.AGENT_LEDGER_TASK_ID.endsWith("-1"),
      `index=1 task id must end with -1: ${out1.AGENT_LEDGER_TASK_ID}`
    );
    assert.ok(
      out0.AGENT_ID.endsWith(":0"),
      `index=0 AGENT_ID must end with :0: ${out0.AGENT_ID}`
    );
    assert.ok(
      out1.AGENT_ID.endsWith(":1"),
      `index=1 AGENT_ID must end with :1: ${out1.AGENT_ID}`
    );
  }
);

// Test 9: the assign call in subagent mode includes --if-absent.
test(
  "assign call in subagent mode includes --if-absent for idempotent retry",
  (t) => {
    const { binDir, stubLog, cleanup } = makeTestEnv();
    t.after(cleanup);
    const env = makeEnv(binDir, stubLog, FULL_SUBAGENT_ENV);

    const result = runBootstrap(env, ["--json"]);
    assert.equal(result.status, 0, `bootstrap failed:\n${result.stderr}`);

    const log = readFileSync(stubLog, "utf8");
    // The bootstrap first probes assign --help, then makes the real call.
    // Find the assign invocation that is not the capability probe.
    const assignLine = log
      .split("\n")
      .find((l) => l.startsWith("assign ") && !l.includes("--help"));
    assert.ok(
      assignLine,
      `no real assign invocation found in stub log:\n${log}`
    );
    assert.ok(
      assignLine.includes("--if-absent"),
      `assign call must include --if-absent for idempotent retry:\n${assignLine}`
    );
  }
);
