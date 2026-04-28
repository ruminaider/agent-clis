-- 0002_unique_active_assignment: enforce one active assignment per
-- (task_id, assigned_agent_id) pair. SPEC §16 treats an assignment as
-- the orchestrator's contract for a task. Two simultaneously-active
-- assignments for the same (task, agent) pair were never well-defined
-- but were possible because nothing in the schema ruled it out.
-- F9 in PR #8 exposed this as a SELECT-then-INSERT race in
-- agent-ledger assign --if-absent that could produce duplicate active
-- rows under concurrent bootstraps.
--
-- This migration:
--   1. Deduplicates any existing duplicate active rows by keeping the
--      most-recently-created row per (task_id, COALESCE(agent_id,''))
--      and demoting the older rows to status='superseded' with a
--      closed_at timestamp. Audit trail is preserved (events and
--      audit JSONL are not touched). Reviewers can find the demoted
--      rows with WHERE status='superseded'.
--   2. Adds a partial unique index that prevents future duplicates
--      from being inserted at the storage layer. Callers see
--      "UNIQUE constraint failed" on the second insert; the kernel
--      maps it to a typed assignment_exists error.
--
-- Migration is idempotent: re-applying is a no-op (the UPDATE finds
-- nothing to demote and CREATE UNIQUE INDEX IF NOT EXISTS is safe).

UPDATE assignments
SET status = 'superseded',
    closed_at = COALESCE(closed_at, strftime('%Y-%m-%dT%H:%M:%fZ', 'now'))
WHERE assignment_id IN (
  SELECT assignment_id FROM (
    SELECT assignment_id,
           ROW_NUMBER() OVER (
             PARTITION BY task_id, COALESCE(assigned_agent_id, '')
             ORDER BY created_at DESC, assignment_id DESC
           ) AS rn
    FROM assignments
    WHERE status = 'active'
  )
  WHERE rn > 1
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_assignments_active_task_agent
  ON assignments(task_id, COALESCE(assigned_agent_id, ''))
  WHERE status = 'active';
