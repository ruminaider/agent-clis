# Agent Ledger Walkthrough

This walkthrough drives `agent-ledger` through one full task lifecycle:
project init, identity, assignment, claim, change recording, close,
verify, and clean-checkout summary verification. It is the canonical
example for adapter authors and for new users evaluating the CLI.

All commands assume `agent-ledger` is on your `PATH`. Replace it with
`./bin/agent-ledger` when running from a source build.

## 0. Prerequisites

- A git repository with at least one commit. Worktree discovery and
  project fingerprinting both consult git metadata.
- Write access to a directory the ledger can use. The default is
  `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger/repos/<slug>-<fingerprint>/`.
  Use `--ledger-dir` (or `AGENT_LEDGER_DIR`) to override.

## 1. Initialize the ledger

```bash
LEDGER=$(mktemp -d)
agent-ledger init \
  --project-id local/example \
  --ledger-dir "$LEDGER" \
  --write-pointer
```

`init` creates the SQLite database, the `audit/`, `blobs/`, and
`locks/` subdirectories, writes a local `.agent-ledger.toml` pointer
in the project root, and (when inside a git repo) drops a
`pointer.toml` next to the git common dir so worktrees share the same
ledger. `--project-id` is the stable identifier the ledger uses for
the slug; the fingerprint is derived from git metadata.

## 2. Inspect the environment

```bash
agent-ledger doctor --json --ledger-dir "$LEDGER"
```

`doctor` emits an `agent-ledger.doctor.v1` document with checks for
project identity, ledger writability, git detection, pointer
validity, policy file validity, lock support, adapter environment
variables, SQLite pragmas, and migrations. Use it as a first-line
diagnostic when integrating with a new harness.

## 3. Apply migrations

```bash
agent-ledger migrate --ledger-dir "$LEDGER"
```

`init` applies migrations on first run. `migrate` re-applies them
idempotently. Use `migrate --status` for read-only schema reporting.

## 4. Identify the agent

Every event is attributed to an agent identity. Set `AGENT_ID`
(usually via the harness) and pin the kind for the session:

```bash
export AGENT_ID="worker.alice"
agent-ledger identify --agent-kind worker --harness pi --shell
```

`--shell` prints `export` lines so you can `eval $(...)` the result
inside scripts.

## 5. Create an assignment

The orchestrator declares the contract for a task: which paths an
agent may touch and under what policy.

```bash
agent-ledger assign \
  --task T1 \
  --orchestrator op.main \
  --agent worker.alice \
  --allow 'src/**' \
  --forbid 'src/secrets/**' \
  --policy warn \
  --reason "implement feature X" \
  --ledger-dir "$LEDGER"
```

The reason is stored verbatim for debugging but is rejected if it
matches a secret pattern (AWS keys, GitHub PATs, bearer tokens, etc.).
Audit JSONL and exported summaries reference reasons by SHA-256 only.

## 6. Claim files before editing

```bash
agent-ledger claim src/main.go \
  --task T1 \
  --reason "edit main entrypoint" \
  --json \
  --ledger-dir "$LEDGER"
# Capture the intent ID for later steps.
INTENT=$(agent-ledger claim src/main.go --task T1 --reason "edit main" \
  --json --ledger-dir "$LEDGER" | jq -r .intent_id)
```

Claim policies follow SPEC §16. Under `warn`, overlapping claims
produce a conflict record but both proceed. Under `exclusive`, the
second claim is blocked with exit code 4. Forbidden or
outside-assignment paths are rejected without writing an intent.

Long-running edits should heartbeat:

```bash
agent-ledger heartbeat --intent "$INTENT" --ledger-dir "$LEDGER"
```

## 7. Record changes

After editing, record the actual changes and any validation results:

```bash
agent-ledger record src/main.go \
  --intent "$INTENT" \
  --summary "rename Foo to Bar" \
  --validation "go test ./...:passed" \
  --validation "go vet ./...:passed" \
  --ledger-dir "$LEDGER"
```

Recording a path that is not in the intent's claimed set fails with
exit code 1 and writes no event. Diff content is never persisted by
default; pass `--include-diff --yes` to opt in (the patch goes to a
content-addressed blob, never to the database).

## 8. Close the intent

```bash
agent-ledger close --intent "$INTENT" --outcome completed \
  --ledger-dir "$LEDGER"
```

Outcomes are `completed`, `abandoned`, or `superseded`. Closing
records `intent.closed` and locks further records against the intent.

## 9. Verify the working tree

```bash
agent-ledger verify --json --ledger-dir "$LEDGER"
agent-ledger verify --task T1 --json --ledger-dir "$LEDGER"
```

`verify` returns an `agent-ledger.verify.v1` document. Status is
`passed`, `failed`, `needs-decision`, or `error`. Findings carry a
canonical `code` (see `docs/finding-codes.md`), severity, scalar
`path`, and `suggested_recovery`. Exit codes follow SPEC §19.1.

## 10. Export and verify a task summary

A summary is a portable, privacy-safe receipt for the task. Reviewers
can verify it without local ledger state.

```bash
agent-ledger export-summary \
  --task T1 \
  --output /tmp/T1-summary.json \
  --ledger-dir "$LEDGER"

# Later, in a fresh checkout (no XDG state, no ledger.sqlite needed):
cd /tmp/clean-checkout
git checkout <ref-with-the-changes>
agent-ledger verify --summary /tmp/T1-summary.json --json
```

Path hashes in the summary are project-relative (NFC, forward
slashes), so the same summary verifies identically from any clone or
machine.

## 11. Inspect conflicts and orphans

```bash
agent-ledger conflicts --task T1 --json --ledger-dir "$LEDGER"
agent-ledger conflicts acknowledge <conflict-id> --as-override \
  --ledger-dir "$LEDGER"
agent-ledger gc --stale-after 24h --ledger-dir "$LEDGER"
```

`gc` orphans active intents whose last heartbeat is older than the
window. It writes `intent.orphaned` events; history is never deleted.

## 12. Backfill (when an agent forgot to claim)

```bash
agent-ledger adopt src/forgotten.go \
  --task T1 \
  --agent worker.alice \
  --reason "missed initial claim" \
  --ledger-dir "$LEDGER"
```

Adoption emits `change.adopted` only and stamps
`metadata_json.retroactive = true`. Use sparingly; it bypasses claim
policy.

## Common patterns

- **Per-task verify in CI**: run `agent-ledger verify --task <id> --json`
  in the merge-readiness gate. Treat exit code 4 as a soft block
  (needs-decision); 1, 2, or 3 as hard failures.
- **Cross-machine review**: ship `export-summary` output as a PR
  artifact. Reviewers run `verify --summary` in a clean checkout to
  confirm the diff matches the summary's recorded scope.
- **Long-running claims**: heartbeat at intervals shorter than your
  `gc --stale-after` window. The default GC threshold is whatever the
  orchestrator passes; pick something that survives normal idle time.

## Where to go next

- `docs/exit-codes.md`: every exit code the CLI emits, by command.
- `docs/finding-codes.md`: every `verify` finding code with a one-line
  explanation.
- `docs/packaging.md`: building, releasing, and CI details.
- `SPEC.md`: authoritative design spec including phase plans.
