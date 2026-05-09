import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { buildAssignmentMarker } from "../shared/marker.js";

const here = path.dirname(fileURLToPath(import.meta.url));
const markerSh = path.join(here, "../shared/marker.sh");

test("JS and shell marker helpers emit the same marker", () => {
  const input = {
    by: "pi-extension subagent hook",
    parent: "parent task",
    task: "task/one",
    agent: "worker one",
    effect: "effect:two",
  };
  const js = buildAssignmentMarker(input);
  const sh = execFileSync("bash", [
    markerSh,
    "--by", input.by,
    "--parent", input.parent,
    "--task", input.task,
    "--agent", input.agent,
    "--effect", input.effect,
  ], { encoding: "utf8" });
  assert.equal(sh, js);
  assert.equal(js, "[auto-assigned by pi-extension-subagent-hook auto-derived parent=parent-task task=task/one agent=worker-one effect=effect:two]");
});

test("marker helper requires by", () => {
  assert.throws(() => buildAssignmentMarker({}), /by is required/);
});

test("harness-derived marker uses different prefix and source tag", () => {
  const js = buildAssignmentMarker({ by: "pi-adapter", source: "branch", task: "chore/foo" });
  assert.equal(js, "[harness-derived by pi-adapter source=branch task=chore/foo]");
  const sh = execFileSync("bash", [markerSh, "--by", "pi-adapter", "--source", "branch", "--task", "chore/foo"], { encoding: "utf8" });
  assert.equal(sh, js);
});

test("harness-derived marker for pr source", () => {
  const js = buildAssignmentMarker({ by: "pi-adapter", source: "pr", task: "pr-58" });
  assert.equal(js, "[harness-derived by pi-adapter source=pr task=pr-58]");
});

test("harness-derived marker for detached source", () => {
  const js = buildAssignmentMarker({ by: "pi-adapter", source: "detached", task: "detached/abc1234" });
  assert.equal(js, "[harness-derived by pi-adapter source=detached task=detached/abc1234]");
});

test("harness-derived marker for pointer source has shell parity", () => {
  const input = { by: "pi-adapter", source: "pointer", task: "ambient-2026-05" };
  const js = buildAssignmentMarker(input);
  assert.equal(js, "[harness-derived by pi-adapter source=pointer task=ambient-2026-05]");
  const sh = execFileSync("bash", [markerSh, "--by", input.by, "--source", input.source, "--task", input.task], { encoding: "utf8" });
  assert.equal(sh, js);
});

test("harness-derived marker for subagent source has shell parity", () => {
  const input = {
    by: "pi-adapter",
    source: "subagent",
    task: "parent-task/worker/run-abc-0",
    agent: "agent:pi:subagent:run-abc:0",
  };
  const js = buildAssignmentMarker(input);
  assert.equal(
    js,
    "[harness-derived by pi-adapter source=subagent task=parent-task/worker/run-abc-0 agent=agent:pi:subagent:run-abc:0]",
  );
  const sh = execFileSync("bash", [
    markerSh,
    "--by", input.by,
    "--source", input.source,
    "--task", input.task,
    "--agent", input.agent,
  ], { encoding: "utf8" });
  assert.equal(sh, js);
});

test("unknown source falls back to auto-assigned format", () => {
  const js = buildAssignmentMarker({ by: "pi-adapter", source: "some-future-mode", task: "x" });
  assert(js.startsWith("[auto-assigned by pi-adapter auto-derived"), `got ${js}`);
});

test("explicit source=auto preserves the rc1 marker format", () => {
  const js = buildAssignmentMarker({ by: "pi-adapter", source: "auto", task: "auto/x/y" });
  assert.equal(js, "[auto-assigned by pi-adapter auto-derived task=auto/x/y]");
});
