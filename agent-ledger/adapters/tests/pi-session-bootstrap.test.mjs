/**
 * Load-bearing regression coverage for pi-session task fallback.
 */

import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { spawnSync } from "node:child_process";
import { chmodSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const bootstrapSh = join(here, "../shared/session-bootstrap.sh");

const STUB = `#!/usr/bin/env bash
printf '%s\\n' "$*" >> "$AGENT_LEDGER_STUB_LOG"
case "$1" in
  identify) exit 0 ;;
  assignments) printf '{"assignments":[],"count":0}\\n'; exit 0 ;;
  pointer)
    if [[ "\${2:-}" == "show" ]]; then
      if [[ -n "\${AGENT_LEDGER_STUB_POINTER_JSON:-}" ]]; then
        printf '%s\\n' "$AGENT_LEDGER_STUB_POINTER_JSON"
      else
        printf '%s\\n' '{"present":false}'
      fi
      exit 0
    fi
    ;;
  assign)
    if [[ "\${2:-}" == "--help" ]]; then
      printf 'usage: agent-ledger assign [--metadata]\\n'
      exit 0
    fi
    while [[ $# -gt 0 ]]; do
      case "$1" in
        --metadata) printf '%s\\n' "$2" > "$AGENT_LEDGER_STUB_METADATA_LOG"; shift 2 ;;
        *) shift ;;
      esac
    done
    exit 0 ;;
esac
`;

function makeEnv(t, extra = {}) {
  const dir = mkdtempSync(join(tmpdir(), "pi-session-bootstrap-"));
  const binDir = join(dir, "bin");
  const cwd = join(dir, "nogit");
  const log = join(dir, "ledger.log");
  const metadata = join(dir, "metadata.json");
  mkdirSync(binDir, { recursive: true });
  mkdirSync(cwd, { recursive: true });
  writeFileSync(join(binDir, "agent-ledger"), STUB, { mode: 0o755 });
  chmodSync(join(binDir, "agent-ledger"), 0o755);
  t.after(() => rmSync(dir, { recursive: true, force: true }));
  return {
    cwd,
    log,
    metadata,
    env: {
      PATH: `${binDir}:${process.env.PATH}`,
      HOME: process.env.HOME ?? "/tmp",
      TMPDIR: tmpdir(),
      AGENT_LEDGER_STUB_LOG: log,
      AGENT_LEDGER_STUB_METADATA_LOG: metadata,
      ...extra,
    },
  };
}

function run(env, cwd, flags = []) {
  return spawnSync("bash", [bootstrapSh, "--harness", "pi", "--agent-kind", "worker", "--cwd", cwd, "--json", ...flags], {
    encoding: "utf8",
    env,
  });
}

function output(result) {
  assert.equal(result.status, 0, result.stderr);
  const line = result.stdout.split("\n").find((entry) => entry.startsWith("AGENT_LEDGER_BOOTSTRAP_JSON="));
  assert.ok(line, result.stdout);
  return JSON.parse(line.slice("AGENT_LEDGER_BOOTSTRAP_JSON=".length));
}

test("pi-session fallback is deterministic, auto-assigned, and audited", (t) => {
  const { cwd, env, log, metadata } = makeEnv(t, { PI_SESSION_ID: "ignored-env-session" });
  const sessionId = "pi-session-123";
  const expected = `auto/pi-session/${createHash("sha256").update(sessionId).digest("hex").slice(0, 24)}`;

  const first = output(run(env, cwd, ["--session-id", sessionId]));
  const second = output(run(env, cwd, ["--session-id", sessionId]));
  assert.equal(first.AGENT_LEDGER_TASK_ID, expected);
  assert.equal(second.AGENT_LEDGER_TASK_ID, expected);
  assert.equal(first.AGENT_LEDGER_TASK_SOURCE, "pi-session");
  assert.equal(first.AGENT_LEDGER_AUTO_ASSIGNED, "1");
  assert.match(run(env, cwd, ["--session-id", sessionId]).stderr, /task id from pi-session/);
  assert.match(readFileSync(log, "utf8"), /\[auto-assigned by pi-adapter auto-derived/);
  assert.deepEqual(JSON.parse(readFileSync(metadata, "utf8")), {
    auto_assigned: true,
    auto_assigned_by: "pi-adapter",
    task_source: "pi-session",
  });
});

test("local pointer wins over pi-session, which wins over timestamp fallback", (t) => {
  const pointer = makeEnv(t, {
    PI_SESSION_ID: "pi-session-123",
    AGENT_LEDGER_STUB_POINTER_JSON: '{"present":true,"default_task_id":"ambient-task"}',
  });
  const pointerOutput = output(run(pointer.env, pointer.cwd));
  assert.equal(pointerOutput.AGENT_LEDGER_TASK_ID, "ambient-task");
  assert.equal(pointerOutput.AGENT_LEDGER_TASK_SOURCE, "pointer");

  const session = makeEnv(t, { PI_SESSION_ID: "pi-session-123" });
  const sessionOutput = output(run(session.env, session.cwd));
  assert.equal(sessionOutput.AGENT_LEDGER_TASK_SOURCE, "pi-session");

  const legacy = makeEnv(t);
  const legacyOutput = output(run(legacy.env, legacy.cwd));
  assert.equal(legacyOutput.AGENT_LEDGER_TASK_SOURCE, "auto");
  assert.match(legacyOutput.AGENT_LEDGER_TASK_ID, /^auto\/(?!pi-session\/)/);
});

test("strict mode refuses pi-session fallback", (t) => {
  const { cwd, env } = makeEnv(t, {
    PI_SESSION_ID: "pi-session-123",
    AGENT_LEDGER_REQUIRE_TASK: "1",
  });
  const result = run(env, cwd);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /AGENT_LEDGER_REQUIRE_TASK=1/);
});
