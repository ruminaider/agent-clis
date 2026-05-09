import test from "node:test";
import assert from "node:assert/strict";
import { AUTO_REASON_HINTS, buildAutoFallbackToast } from "../shared/auto-fallback-toast.js";

test("known reason renders the hint inline", () => {
  const out = buildAutoFallbackToast("auto/example/20260509T120000Z", "not_in_git_repo");
  assert.match(out, /^agent-ledger: no task context found; auto task=auto\/example\/20260509T120000Z \(/);
  assert.ok(
    out.includes(AUTO_REASON_HINTS.not_in_git_repo),
    `toast missing hint text: ${out}`,
  );
});

test("unknown reason falls back to (reason=...)", () => {
  const out = buildAutoFallbackToast("auto/example/x", "future_reason_not_yet_mapped");
  assert.equal(
    out,
    "agent-ledger: no task context found; auto task=auto/example/x (reason=future_reason_not_yet_mapped)",
  );
});

test("null reason renders no parenthetical", () => {
  const out = buildAutoFallbackToast("auto/example/x", null);
  assert.equal(out, "agent-ledger: no task context found; auto task=auto/example/x");
});

test("null taskId renders <unknown>", () => {
  const out = buildAutoFallbackToast(null, null);
  assert.equal(out, "agent-ledger: no task context found; auto task=<unknown>");
});

test("null taskId with known reason still renders the hint", () => {
  const out = buildAutoFallbackToast(null, "git_no_head");
  assert.ok(out.startsWith("agent-ledger: no task context found; auto task=<unknown> ("));
  assert.ok(out.includes(AUTO_REASON_HINTS.git_no_head));
});

test("AUTO_REASON_HINTS covers every reason emitted by session-bootstrap.sh", () => {
  // Keep this list in sync with the AUTO_REASON branches in
  // adapters/shared/session-bootstrap.sh. A drift here means a real
  // operator will see (reason=<token>) instead of an actionable hint.
  const expected = [
    "not_in_git_repo",
    "git_no_head",
    "pointer_lacks_default",
    "pointer_unreadable",
    "pointer_parser_unavailable",
  ];
  for (const r of expected) {
    assert.ok(
      typeof AUTO_REASON_HINTS[r] === "string" && AUTO_REASON_HINTS[r].length > 0,
      `AUTO_REASON_HINTS missing actionable hint for ${r}`,
    );
  }
});
