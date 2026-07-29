/**
 * Exercises the pi extension's bootstrap boundary without requiring a Pi
 * runtime. Node strips the extension's TypeScript types, then a minimal Pi
 * hook registry invokes the real tool_call handler.
 */

import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { stripTypeScriptTypes } from "node:module";

const here = dirname(fileURLToPath(import.meta.url));
const extensionPath = join(here, "../pi/agent-ledger.ts");
const extensionSource = await readFile(extensionPath, "utf8");
const extensionModuleSource = stripTypeScriptTypes(extensionSource, { mode: "transform" });
let moduleNonce = 0;

const CONTROLLED_ENV = [
  "HOME",
  "AGENT_ID",
  "AGENT_LEDGER_TASK_ID",
  "AGENT_LEDGER_REQUIRE_TASK",
  "AGENT_LEDGER_DETECT_PR",
  "PI_SUBAGENT_CHILD",
  "PI_SUBAGENT_RUN_ID",
  "PI_SUBAGENT_CHILD_INDEX",
  "PI_SUBAGENT_CHILD_AGENT",
];

async function loadExtension() {
  const dir = mkdtempSync(join(tmpdir(), "pi-extension-module-"));
  const modulePath = join(dir, `agent-ledger-${moduleNonce++}.mjs`);
  writeFileSync(modulePath, extensionModuleSource);
  try {
    return await import(pathToFileURL(modulePath).href);
  } finally {
    rmSync(dir, { recursive: true, force: true });
  }
}

async function registerToolHandlers(t, env = {}, setupHome = () => {}) {
  const home = mkdtempSync(join(tmpdir(), "pi-bootstrap-gating-"));
  const original = new Map(CONTROLLED_ENV.map((key) => [key, process.env[key]]));
  t.after(() => {
    for (const [key, value] of original) {
      if (value === undefined) delete process.env[key];
      else process.env[key] = value;
    }
    rmSync(home, { recursive: true, force: true });
  });

  for (const key of CONTROLLED_ENV) delete process.env[key];
  process.env.HOME = home;
  Object.assign(process.env, env);
  setupHome(home);

  const extension = await loadExtension();
  let toolCallHandler;
  let toolResultHandler;
  extension.default({
    on(eventName, handler) {
      if (eventName === "tool_call") toolCallHandler = handler;
      if (eventName === "tool_result") toolResultHandler = handler;
    },
  });
  assert.equal(typeof toolCallHandler, "function");
  assert.equal(typeof toolResultHandler, "function");
  return { toolCallHandler, toolResultHandler };
}

async function registerToolCallHandler(t, env = {}) {
  return (await registerToolHandlers(t, env)).toolCallHandler;
}

const noUiContext = { hasUI: false };

test("only file-changing tools, bash, and execution subagents require bootstrap", async () => {
  const { requiresBootstrapForTool } = await loadExtension();

  for (const toolName of ["write", "edit", "multi_edit", "multiedit", "bash"]) {
    assert.equal(requiresBootstrapForTool(toolName, {}), true, `${toolName} must bootstrap`);
  }
  assert.equal(requiresBootstrapForTool("subagent", {}), true);
  assert.equal(requiresBootstrapForTool("subagent", { action: "list" }), false);
  assert.equal(requiresBootstrapForTool("read", { path: "README.md" }), false);
  assert.equal(requiresBootstrapForTool("find", { query: "task" }), false);
});

test("strict task enforcement does not bootstrap reads or subagent management", async (t) => {
  const toolCall = await registerToolCallHandler(t, { AGENT_LEDGER_REQUIRE_TASK: "1" });

  assert.equal(
    await toolCall({ toolName: "read", toolCallId: "read-1", input: { path: "README.md" } }, noUiContext),
    undefined,
  );
  assert.equal(
    await toolCall({ toolName: "subagent", toolCallId: "subagent-list", input: { action: "list" } }, noUiContext),
    undefined,
  );

  const edit = await toolCall({ toolName: "edit", toolCallId: "edit-1", input: { path: "README.md" } }, noUiContext);
  assert.equal(edit?.block, true, "an edit must still enforce bootstrap failure in strict mode");
});

test("a failed eager child bootstrap blocks edits but never read-only tools", async (t) => {
  const originalConsoleError = console.error;
  console.error = () => {};
  t.after(() => { console.error = originalConsoleError; });
  const toolCall = await registerToolCallHandler(t, { PI_SUBAGENT_CHILD: "1" });

  // Force the eager bootstrap attempt to settle through an enforcement-bound
  // call. The isolated HOME intentionally has no bootstrap helper.
  const firstEdit = await toolCall({ toolName: "edit", toolCallId: "edit-1", input: { path: "README.md" } }, noUiContext);
  assert.equal(firstEdit?.block, true);

  assert.equal(
    await toolCall({ toolName: "read", toolCallId: "read-1", input: { path: "README.md" } }, noUiContext),
    undefined,
    "read-only calls must return before eager-bootstrap failure blocking",
  );

  const laterEdit = await toolCall({ toolName: "edit", toolCallId: "edit-2", input: { path: "README.md" } }, noUiContext);
  assert.equal(laterEdit?.block, true, "edits must remain blocked after eager bootstrap failure");
});

test("Bash retries unavailable pre-scans and resumes attribution when Git becomes available", async (t) => {
  const originalPath = process.env.PATH;
  const originalGitCallLog = process.env.GIT_CALL_LOG;
  const originalGitAvailable = process.env.GIT_AVAILABLE;
  const originalCwd = process.cwd();
  const workDir = mkdtempSync(join(tmpdir(), "pi-bash-no-git-"));
  const binDir = mkdtempSync(join(tmpdir(), "pi-bash-no-git-bin-"));
  const gitLog = join(binDir, "git.log");
  const errors = [];
  const originalConsoleError = console.error;
  console.error = (message) => errors.push(String(message));
  t.after(() => {
    console.error = originalConsoleError;
    process.env.PATH = originalPath;
    if (originalGitCallLog === undefined) delete process.env.GIT_CALL_LOG;
    else process.env.GIT_CALL_LOG = originalGitCallLog;
    if (originalGitAvailable === undefined) delete process.env.GIT_AVAILABLE;
    else process.env.GIT_AVAILABLE = originalGitAvailable;
    process.chdir(originalCwd);
    rmSync(workDir, { recursive: true, force: true });
    rmSync(binDir, { recursive: true, force: true });
  });

  writeFileSync(join(binDir, "git"), `#!/usr/bin/env bash
if [[ "\${GIT_AVAILABLE:-0}" == "1" ]]; then
  printf 'available\\n' >> "$GIT_CALL_LOG"
  exit 0
fi
printf 'unavailable\\n' >> "$GIT_CALL_LOG"
printf 'fatal: not a git repository (or any of the parent directories): .git\\n' >&2
exit 128
`);
  writeFileSync(join(binDir, "agent-ledger"), "#!/usr/bin/env bash\nexit 0\n");
  for (const executable of ["git", "agent-ledger"]) {
    chmodSync(join(binDir, executable), 0o755);
  }

  const { toolCallHandler, toolResultHandler } = await registerToolHandlers(
    t,
    {},
    (home) => {
      const bootstrapDir = join(home, ".pi/agent/extensions/agent-ledger");
      mkdirSync(bootstrapDir, { recursive: true });
      writeFileSync(
        join(bootstrapDir, "session-bootstrap.sh"),
        "#!/usr/bin/env bash\nprintf '%s\\n' 'AGENT_LEDGER_BOOTSTRAP_JSON={\"AGENT_ID\":\"test-agent\",\"AGENT_LEDGER_TASK_ID\":\"test-task\",\"AGENT_LEDGER_TASK_SOURCE\":\"env\",\"AGENT_LEDGER_AUTO_ASSIGNED\":\"0\"}'\n",
        { mode: 0o755 },
      );
    },
  );
  process.env.PATH = `${binDir}:${originalPath}`;
  process.env.GIT_CALL_LOG = gitLog;
  process.chdir(workDir);

  for (const id of ["bash-1", "bash-2"]) {
    assert.equal(
      await toolCallHandler({ toolName: "bash", toolCallId: id, input: { command: "true" } }, noUiContext),
      undefined,
    );
    await toolResultHandler({ toolName: "bash", toolCallId: id, input: { command: "true" }, isError: false }, noUiContext);
  }

  process.env.GIT_AVAILABLE = "1";
  assert.equal(
    await toolCallHandler({ toolName: "bash", toolCallId: "bash-3", input: { command: "true" } }, noUiContext),
    undefined,
  );
  await toolResultHandler({ toolName: "bash", toolCallId: "bash-3", input: { command: "true" }, isError: false }, noUiContext);

  assert.equal(
    readFileSync(gitLog, "utf8"),
    "unavailable\nunavailable\navailable\navailable\n",
    "each unavailable Bash call must retry its pre-scan, skip its matching post-scan, and later Git calls must scan before and after execution",
  );
  assert.equal(
    errors.filter((message) => message === "agent-ledger: Bash change attribution is unavailable because this session is outside a Git repository.").length,
    1,
    "the unavailable-attribution notice should appear once per session",
  );
  assert.equal(errors.some((message) => message.includes("fatal: not a git repository")), false);
});
