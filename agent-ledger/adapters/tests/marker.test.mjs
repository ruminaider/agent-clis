import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { fileURLToPath } from "node:url";
import path from "node:path";
import { buildAutoAssignedMarker } from "../shared/marker.js";

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
  const js = buildAutoAssignedMarker(input);
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

test("marker helper requires source", () => {
  assert.throws(() => buildAutoAssignedMarker({}), /by is required/);
});
