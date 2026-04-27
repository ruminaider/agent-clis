import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, cpSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";

const root = path.resolve(import.meta.dirname, "../..");

async function importWrapperWithStub() {
  const tmp = mkdtempSync(path.join(tmpdir(), "agent-ledger-wrapper-test-"));
  const wrapperDir = path.join(tmp, "adapters/babysitter");
  const sharedDir = path.join(tmp, "adapters/shared");
  const stubDir = path.join(tmp, "node_modules/@a5c-ai/babysitter-sdk");
  mkdirSync(wrapperDir, { recursive: true });
  mkdirSync(sharedDir, { recursive: true });
  mkdirSync(stubDir, { recursive: true });
  cpSync(path.join(root, "adapters/shared/marker.js"), path.join(sharedDir, "marker.js"));
  writeFileSync(path.join(stubDir, "package.json"), JSON.stringify({ type: "module", exports: "./index.js" }));
  writeFileSync(path.join(stubDir, "index.js"), "export function defineTask(name, factory) { return { name, run: factory }; }\n");
  writeFileSync(
    path.join(wrapperDir, "define-ledger-task.js"),
    readFileSync(path.join(root, "adapters/babysitter/define-ledger-task.js")),
  );
  const mod = await import(pathToFileURL(path.join(wrapperDir, "define-ledger-task.js")));
  return { mod, cleanup: () => rmSync(tmp, { recursive: true, force: true }) };
}

test("shellQuote returns one quoted token without trailing whitespace", async () => {
  const { mod, cleanup } = await importWrapperWithStub();
  try {
    assert.equal(mod.shellQuote("abc"), "'abc'");
    assert.equal(mod.shellQuote("a'b"), "'a'\\''b'");
    assert.equal(/\s$/.test(mod.shellQuote("abc")), false);
  } finally {
    cleanup();
  }
});

test("defineLedgerTask wraps assign, inner task, verify, and gates auto marker", async () => {
  const { mod, cleanup } = await importWrapperWithStub();
  try {
    const task = mod.defineLedgerTask("demo", (args) => ({
      kind: "agent",
      title: "Inner",
      execution: { env: { EXISTING: "1" } },
      args,
    }));

    const auto = task.run({ reason: "do work", allow: ["src/**", "tests/**"] }, { effectId: "fx1" });
    assert.equal(auto.kind, "chain");
    assert.equal(auto.steps.length, 3);
    assert.match(auto.steps[0].shell.command, /agent-ledger assign/);
    assert.match(auto.steps[0].shell.command, /'--allow' 'src\/\*\*' '--allow' 'tests\/\*\*'/);
    assert.match(auto.steps[0].shell.command, /\[auto-assigned by babysitter-wrapper auto-derived task=demo agent=babysitter\/demo effect=fx1\]/);
    assert.equal(auto.steps[1].execution.env.AGENT_LEDGER_TASK_ID, "auto/demo/fx1");
    assert.equal(auto.steps[1].execution.env.EXISTING, "1");
    assert.match(auto.steps[2].shell.command, /agent-ledger verify/);

    const explicit = task.run({ taskId: "task-123", reason: "explicit reason" }, { effectId: "fx2" });
    assert.doesNotMatch(explicit.steps[0].shell.command, /\[auto-assigned/);
    assert.match(explicit.steps[0].shell.command, /'explicit reason'/);
  } finally {
    cleanup();
  }
});
