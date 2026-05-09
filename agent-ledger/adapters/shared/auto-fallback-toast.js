// auto-fallback-toast.js renders the user-visible toast text for the
// adapter's auto-task fallback. It lives in the shared adapter
// directory (rather than inside the pi-specific TypeScript extension)
// so other adapters and a node-based test suite can import it without
// a TypeScript loader. The pi extension re-exports buildAutoFallbackToast
// from this module to keep its public surface intact.
//
// AUTO_REASON_HINTS expands the AGENT_LEDGER_TASK_AUTO_REASON tokens
// emitted by adapters/shared/session-bootstrap.sh into actionable
// guidance the toast renders directly. Keep this map in sync with the
// reasons surfaced by that script's auto-fallback branch.

const AUTO_REASON_HINTS = Object.freeze({
  not_in_git_repo:
    "cwd is not inside a git checkout. Set AGENT_LEDGER_TASK_ID, declare default_task_id in .agent-ledger.toml, or launch from inside a git checkout.",
  git_no_head:
    "git repo has no branch and no resolvable HEAD. Set AGENT_LEDGER_TASK_ID or declare default_task_id in .agent-ledger.toml.",
  pointer_lacks_default:
    "local .agent-ledger.toml does not declare default_task_id. Add it, or set AGENT_LEDGER_TASK_ID.",
  pointer_unreadable:
    "local .agent-ledger.toml exists but cannot be parsed; agent-ledger pointer show failed. Fix the file (run `agent-ledger pointer show` to see the error), or set AGENT_LEDGER_TASK_ID.",
  pointer_parser_unavailable:
    "local .agent-ledger.toml is present but neither python3 nor node is on PATH to parse the kernel's JSON projection. Install python3 or node, or set AGENT_LEDGER_TASK_ID.",
});

function buildAutoFallbackToast(taskId, reason) {
  const head = `agent-ledger: no task context found; auto task=${taskId == null ? "<unknown>" : taskId}`;
  const hint = reason ? AUTO_REASON_HINTS[reason] : undefined;
  if (hint) return `${head} (${hint})`;
  if (reason) return `${head} (reason=${reason})`;
  return head;
}

export { AUTO_REASON_HINTS, buildAutoFallbackToast };
