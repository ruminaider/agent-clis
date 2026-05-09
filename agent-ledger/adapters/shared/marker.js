// Shared assignment-source marker helper.
//
// Two marker formats:
//
// - "auto-assigned": emitted when the bootstrap could not derive a task
//   id from harness context (no env var, no git branch, no PR) and had
//   to fall back to a synthetic timestamp id. Reviewers querying for
//   sessions where the orchestrator forgot to assign filter on
//   `[auto-assigned`.
//
// - "harness-derived": emitted when the bootstrap derived the task id
//   from a meaningful source the harness already knew (git branch, PR
//   number, detached HEAD short SHA). These are not failures; they are
//   the harness doing its job. Reviewers can filter on
//   `[harness-derived` and group by `source=...`.
//
// Assignments without either prefix were supplied explicitly by an
// orchestrator (env var or --task-id flag).
//
// Subagent assignment metadata schema (authoritative).
//
// When a pi subagent child bootstraps and calls
// `agent-ledger assign --metadata <json>`, the JSON payload must carry
// the six fields below. Programmatic readers (verify, audit tooling,
// cross-tool correlation) treat this metadata, not the reason-text
// marker, as the authoritative surface. The reason-text marker remains
// an audit hint.
//
// @typedef {object} SubagentAssignmentMetadata
// @property {string} parent_task         Inherited parent task id
//                                        (`AGENT_LEDGER_TASK_ID` from
//                                        the parent process).
// @property {string} parent_agent_id     Inherited parent `AGENT_ID`.
// @property {string} subagent_run_id     `PI_SUBAGENT_RUN_ID` verbatim.
// @property {number} subagent_child_index `PI_SUBAGENT_CHILD_INDEX`
//                                         parsed as a decimal integer
//                                         (JSON number type).
// @property {string} subagent_child_agent `PI_SUBAGENT_CHILD_AGENT`
//                                         verbatim.
// @property {"pi-subagent-bootstrap"} dispatch_origin
//                                         Discriminator literal that
//                                         `verify` reads to suppress
//                                         the `AUTO_ASSIGNED_TASK`
//                                         finding for subagent
//                                         children.

const HARNESS_DERIVED_SOURCES = new Set(["branch", "pr", "detached", "subagent", "pointer"]);

export function buildAssignmentMarker({ by, parent, task, agent, effect, source } = {}) {
  if (!by) throw new Error("buildAssignmentMarker: by is required");
  const sourceTag = (source ?? "auto").toLowerCase();
  if (HARNESS_DERIVED_SOURCES.has(sourceTag)) {
    const parts = [`[harness-derived by ${sanitizeToken(by)}`, `source=${sanitizeToken(sourceTag)}`];
    if (parent) parts.push(`parent=${sanitizeToken(parent)}`);
    if (task) parts.push(`task=${sanitizeToken(task)}`);
    if (agent) parts.push(`agent=${sanitizeToken(agent)}`);
    if (effect) parts.push(`effect=${sanitizeToken(effect)}`);
    return `${parts.join(" ")}]`;
  }
  // Default and "auto" source preserve the v0.2.0-rc1 marker format.
  const parts = [`[auto-assigned by ${sanitizeToken(by)}`, "auto-derived"];
  if (parent) parts.push(`parent=${sanitizeToken(parent)}`);
  if (task) parts.push(`task=${sanitizeToken(task)}`);
  if (agent) parts.push(`agent=${sanitizeToken(agent)}`);
  if (effect) parts.push(`effect=${sanitizeToken(effect)}`);
  return `${parts.join(" ")}]`;
}

function sanitizeToken(value) {
  return String(value).replace(/[^A-Za-z0-9._:@/-]/g, "-");
}

export { sanitizeToken };
