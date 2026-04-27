# Agent Ledger Phase 1 Task Packet

## Context

Phase 1 implements the Agent Ledger kernel slice from `SPEC.md`: a Go static CLI, SQLite-backed storage, JSONL audit mirror, privacy-safe event recording, core coordination commands, verification JSON contract, tests, and packaging basics.

Out of scope for Phase 1:

- pi adapter implementation.
- Babysitter workflow integration beyond kernel `export-summary` and `verify --summary`.
- Claude Code hooks.
- Generic `agent-ledger exec` implementation.
- Remote sync, daemon, dashboard, event signing, and Windows support.

Readiness status: `SPEC.md` passed the implementation-readiness review for the Phase 1 kernel slice. Remaining notes are non-blocking and can be addressed during implementation.

## Review discipline

- Every worker task must create a completion commit before review.
- Every worker task must pass `gate-reviewer` before the orchestrator marks it complete.
- Each wave ends with `wave-review-orchestrator` after all worker tasks in that wave have passed `gate-reviewer`.
- `wave-review-orchestrator` must not auto-execute fixes. It returns findings and, if needed, a remediation packet.
- `SPEC.md` is read-only for all implementation tasks unless the orchestrator explicitly authorizes a spec correction.

## Wave 1: Foundation

### Task 001: Go CLI scaffold

**Agent:** `worker`

**Depends on:** none.

**Goal:** Create the initial Go CLI scaffold for the `agent-ledger` executable.

**Allowed files/directories:**

- `go.mod`, `go.sum`
- `cmd/agent-ledger/**`
- `internal/cli/**`
- `internal/version/**`
- `README.md`
- `.gitignore`
- `Makefile`

**Requirements:**

- Build executable entrypoint at `cmd/agent-ledger`.
- Register Phase 1 commands: `init`, `identify`, `assign`, `claim`, `heartbeat`, `record`, `close`, `status`, `verify`, `conflicts`, `adopt`, `export-summary`, `gc`, `migrate`, `doctor`.
- Add global `--help`, `--version`, and command help.
- Define exit code constants from `SPEC.md` section 19.1.
- Add shared JSON/text error envelope helpers.
- Add `Makefile` targets: `fmt`, `test`, `build`, `check`.
- Add `.gitignore` entries for build outputs and local `.agent-ledger.toml` pointer files.
- Keep README minimal but useful, using the concentric-circles pattern.

**Acceptance criteria:**

- `go test ./...` passes.
- `go run ./cmd/agent-ledger --help` shows the command list.
- `go run ./cmd/agent-ledger --version` prints a non-empty version.
- `go build ./cmd/agent-ledger` succeeds.
- Stubbed commands return clear not-yet-implemented errors only where later tasks own behavior.

**Suggested commands:**

```bash
gofmt -w $(find . -name '*.go')
go test ./...
go run ./cmd/agent-ledger --help
go run ./cmd/agent-ledger --version
go build ./cmd/agent-ledger
make check
```

**Completion commit:** `Scaffold Agent Ledger Go CLI`

---

### Task 002: Project discovery, config, storage resolution, and path normalization

**Agent:** `worker`

**Depends on:** Task 001. May run in parallel with Task 003 only after storage interfaces are agreed.

**Allowed files/directories:**

- `internal/config/**`
- `internal/project/**`
- `internal/paths/**`
- `internal/git/**`
- `internal/storage/**` only for shared interfaces
- `cmd/agent-ledger/**` only for `init` and `doctor` wiring
- `testdata/**`

**Requirements:**

- Resolve ledger directory in spec order: `$AGENT_LEDGER_DIR`, local `.agent-ledger.toml`, XDG state, git common-dir pointer/fallback.
- Support default XDG path: `$XDG_STATE_HOME/agent-ledger/repos/<project-slug>-<project-fingerprint>/`, with `~/.local/state` fallback.
- Compute project fingerprint from `project_id`, origin URL, git common dir realpath, and non-git root realpath.
- Generate 24-character lowercase hex project fingerprint and sanitized project slug.
- Load local `.agent-ledger.toml` and optional committed `.agent-ledger-policy.toml`.
- Implement `init --write-pointer` behavior for local pointer and git common-dir symlink or `pointer.toml` fallback.
- Implement path normalization: root resolution, absolute conversion, symlink resolution where possible, project-relative display path, Unicode NFC, `/` separators, preserved display case, and realpath-normalized `path_hash`.
- Detect outside-project paths.

**Acceptance criteria:**

- Unit tests cover fingerprinting, XDG fallback, env override, pointer override, git common-dir discovery, non-git fallback, slug sanitization, outside-project paths, symlinks, and Unicode NFC.
- `agent-ledger init --project-id <id> --ledger-dir <tmpdir> --write-pointer` creates expected storage and pointer files in an isolated temp project.
- `agent-ledger doctor --json` reports project identity and ledger path without requiring a populated database.
- No config or pointer output includes secrets or environment dumps.

**Suggested commands:**

```bash
go test ./internal/config ./internal/project ./internal/paths ./internal/git ./...
go run ./cmd/agent-ledger init --project-id example.com/test/repo --ledger-dir "$(mktemp -d)" --write-pointer
go run ./cmd/agent-ledger doctor --json
make check
```

**Completion commit:** `Implement ledger storage resolution and path normalization`

---

### Task 003: SQLite storage, migrations, events, and audit mirror

**Agent:** `worker`

**Depends on:** Task 001. Depends on Task 002 interfaces for resolved ledger directory and project identity.

**Allowed files/directories:**

- `internal/storage/**`
- `internal/migrations/**`
- `internal/events/**`
- `internal/audit/**`
- `internal/id/**`
- `cmd/agent-ledger/**` only for `migrate` and storage initialization wiring
- `testdata/**`

**Requirements:**

- Choose a SQLite driver suitable for static Go builds. Prefer pure Go unless a clear reason exists.
- Create layout: `ledger.sqlite`, `audit/YYYY-MM-DD.jsonl`, `blobs/sha256/**`, `locks/**`, and config as needed.
- Apply SQLite pragmas: WAL, `synchronous=NORMAL`, foreign keys on, busy timeout 5000.
- Implement idempotent migrations for all Phase 1 tables from `SPEC.md` section 11.
- Generate IDs as `<type>_<ulid>`.
- Store timestamps as RFC3339 UTC strings with `Z` suffix.
- Store all `*_sha256` values as full 64-character lowercase hex.
- Every domain write helper inserts a domain row, an `events` row, and an audit JSONL line.
- `payload_json` must be privacy-safe. No raw hook inputs, raw tool payloads, command output, environment variables, file contents, full diffs, headers, tokens, or secrets.
- Rotate audit files by UTC date with injectable time for tests.

**Acceptance criteria:**

- `agent-ledger migrate` creates all tables and indexes in a temp ledger and is idempotent.
- Tests cover migrations, schema version tracking, WAL behavior where observable, FK enforcement, ID shape, timestamps, audit writes, and privacy-safe payload handling.
- Simulated domain write creates matching domain, `events`, and audit rows.
- Busy database behavior fails through storage error handling instead of hanging indefinitely.

**Suggested commands:**

```bash
go test ./internal/storage ./internal/migrations ./internal/events ./internal/audit ./...
go run ./cmd/agent-ledger init --ledger-dir "$(mktemp -d)"
go run ./cmd/agent-ledger migrate
make check
```

**Completion commit:** `Add SQLite migrations and audit event store`

---

### Task 004: Wave 1 review

**Agent:** `wave-review-orchestrator`

**Depends on:** Tasks 001, 002, and 003 passing `gate-reviewer`.

**Review scope:** CLI scaffold, project/storage resolution, path normalization, SQLite migrations, event store, audit mirror, and privacy-safe persistence baseline.

**Required evidence:**

- `go test ./...`
- `go build ./cmd/agent-ledger`
- Representative `init`, `migrate`, and `doctor --json` commands in temp directories.

**Routing:**

- Implementation defects go to `worker` with the relevant task ID.
- Sequencing or decomposition defects go to `task-generator`.
- Architectural concerns, such as SQLite driver or static packaging trade-offs, go to `oracle`.

## Wave 2: Command workflows

### Task 005: Identity, assignment, claim lifecycle, conflicts, and status

**Agent:** `worker`

**Depends on:** Wave 1 review with no blocking findings.

**Allowed files/directories:**

- `cmd/agent-ledger/**`
- `internal/commands/**`
- `internal/domain/**`
- `internal/conflicts/**`
- `internal/storage/**` only for query/write methods
- `internal/events/**` only for event constructors
- `internal/paths/**` only for path policy integration
- `testdata/**`

**Requirements:**

- Implement `identify`, including `--shell`.
- Implement `assign` and write `task.assigned`.
- Implement `claim` with `--task`, `--reason`, `--access-mode`, `--policy`, `--supersede`, `--override-conflict`, `--agent`, and `--json`.
- Enforce missing assignment, allowed paths, forbidden paths, overlap detection, `warn`, `exclusive`, and orchestrator override semantics.
- Implement `heartbeat`, `close`, `conflicts`, `conflicts acknowledge [--as-override]`, and `status` variants.
- Ensure all domain writes create events and audit rows.

**Acceptance criteria:**

- Two agents can identify, receive assignments, claim disjoint files, heartbeat, close, and view status.
- `warn` overlap creates an intent and conflict record deterministically.
- `exclusive` overlap blocks the second intent and returns exit code 4 until override rules are satisfied.
- Scope enforcement rejects forbidden and outside-assignment claim paths without creating intent events.

**Suggested commands:**

```bash
go test ./internal/domain ./internal/conflicts ./internal/commands ./...
go run ./cmd/agent-ledger identify --agent-kind worker --harness pi --shell
go run ./cmd/agent-ledger assign --task W1-A --orchestrator pi.main.test --agent pi.worker.test --allow README.md --policy warn --reason "test assignment"
go run ./cmd/agent-ledger claim README.md --task W1-A --agent pi.worker.test --reason "test claim" --json
go run ./cmd/agent-ledger status --json
make check
```

**Completion commit:** `Implement identity assignment claim and conflict commands`

---

### Task 006: Change recording, validation recording, adoption, and summaries

**Agent:** `worker`

**Depends on:** Wave 1 review. Coordinates with Task 005 for intent semantics.

**Allowed files/directories:**

- `cmd/agent-ledger/**`
- `internal/commands/**`
- `internal/domain/**`
- `internal/changes/**`
- `internal/privacy/**`
- `internal/summary/**`
- `internal/storage/**` only for query/write methods
- `internal/events/**` only for event constructors
- `internal/paths/**` only for hashing/normalization integration
- `testdata/**`

**Requirements:**

- Implement `record <path>... --intent <intent-id> --summary <summary> [--validation <command>:<status>]... [--include-diff] [--yes]`.
- Fail with exit code 1 and write no event if any supplied path is not in the intent's claimed paths.
- Store `changes`, `change_paths`, `change.recorded`, optional `validations`, and `validation.recorded` events.
- Parse validation status after the last colon.
- Do not store full diffs, file contents, command output, env dumps, headers, tokens, or secrets by default.
- Implement `adopt` as `change.adopted` plus `changes.metadata_json.retroactive = true`, without also emitting `change.recorded`.
- Implement `export-summary --task <task> --output <path>` with `agent-ledger-summary.v1`, assignment snapshot, changed paths, path hashes, validations, and closed state.

**Acceptance criteria:**

- Claimed file can be recorded with summary and validations.
- Recording an unclaimed path fails without writing a change event.
- Adoption has exactly retroactive adoption semantics.
- Exported summaries are privacy-safe and contain enough assignment data for clean-checkout verification.
- `--include-diff` never silently stores patch text and requires `--yes` in non-interactive use.

**Suggested commands:**

```bash
go test ./internal/changes ./internal/privacy ./internal/summary ./internal/commands ./...
go run ./cmd/agent-ledger record README.md --intent <intent-id> --summary "test record" --validation "go test ./...:passed"
go run ./cmd/agent-ledger adopt README.md --task W1-A --agent pi.worker.test --reason "Backfill missed claim"
go run ./cmd/agent-ledger export-summary --task W1-A --output "$(mktemp -d)/W1-A.json"
make check
```

**Completion commit:** `Implement privacy-safe recording and task summaries`

---

### Task 007: Operational commands: migrate, gc, and doctor

**Agent:** `worker`

**Depends on:** Wave 1 review.

**Allowed files/directories:**

- `cmd/agent-ledger/**`
- `internal/commands/**`
- `internal/gc/**`
- `internal/doctor/**`
- `internal/storage/**`
- `internal/config/**`
- `internal/git/**`
- `internal/locks/**`
- `testdata/**`

**Requirements:**

- Complete `migrate` as explicit schema migration/status command.
- Implement `gc --stale-after <duration>` to mark stale active intents as orphaned via `intent.orphaned`, without deleting history.
- Implement `doctor` and `doctor --json`.
- Doctor checks storage resolution, SQLite health, git detection, project pointer validity, policy file validity, lock support, and adapter env vars.
- Distinguish configuration errors from storage errors with exit codes 2 and 3.

**Acceptance criteria:**

- `migrate` is idempotent and reports useful status.
- `gc` marks expired intents orphaned and writes events without deleting history.
- `doctor --json` emits machine-readable checks and findings.
- Tests cover duration parsing, stale intent marking, pointer validation, lock reporting, policy validation, and DB health failure paths.

**Suggested commands:**

```bash
go test ./internal/gc ./internal/doctor ./internal/commands ./...
go run ./cmd/agent-ledger migrate
go run ./cmd/agent-ledger gc --stale-after 24h
go run ./cmd/agent-ledger doctor --json
make check
```

**Completion commit:** `Implement doctor migrate and stale intent GC`

---

### Task 008: Wave 2 review

**Agent:** `wave-review-orchestrator`

**Depends on:** Tasks 005, 006, and 007 passing `gate-reviewer`.

**Review scope:** Core command behavior, conflict detection, event/audit consistency, privacy-safe recording, stale intent handling, and task summary export.

**Required evidence:** representative end-to-end local flows for assign, claim, record, close, status, conflict acknowledgement, summary export, gc, and doctor.

## Wave 3: Verification, tests, and packaging

### Task 009: Verification contract

**Agent:** `worker`

**Depends on:** Wave 2 review with no blocking findings.

**Allowed files/directories:**

- `cmd/agent-ledger/**`
- `internal/verify/**`
- `internal/policy/**`
- `internal/summary/**`
- `internal/domain/**`
- `internal/storage/**` only for verification queries
- `internal/paths/**`
- `testdata/**`

**Requirements:**

- Implement `verify --json`, `verify --task <task> --json`, and `verify --summary <summary> --json`.
- Emit `agent-ledger.verify.v1` schema from `SPEC.md` section 19.2.
- Implement statuses, severities, finding codes, and exit codes from section 19.
- Detect unclaimed changes, forbidden/outside-assignment paths, active conflicts, stale/open intents, missing reason, missing assignment, agent mismatch, review-only writes, exclusive lock conditions, config errors, storage errors, and summary mismatch.
- Verify summary files without local XDG ledger state by using assignment snapshots.
- Return recovery text consistent with section 25.

**Acceptance criteria:**

- Clean assigned, claimed, recorded, closed flow returns exit code 0 and status `passed`.
- Unclaimed changes report `UNCLAIMED_CHANGE`.
- Forbidden and outside-assignment changes report the correct codes.
- `verify --summary` works in a clean temp checkout and catches tampering as `SUMMARY_MISMATCH`.
- Config and storage failures map to exit codes 2 and 3.

**Suggested commands:**

```bash
go test ./internal/verify ./internal/policy ./internal/summary ./...
go run ./cmd/agent-ledger verify --json
go run ./cmd/agent-ledger verify --task W1-A --json
go run ./cmd/agent-ledger verify --summary tasks/agent-ledger/W1-A.json --json
make check
```

**Completion commit:** `Implement verify JSON contract`

---

### Task 010: Phase 1 test suite

**Agent:** `worker`

**Depends on:** Task 009 or stable verification interfaces.

**Allowed files/directories:**

- `internal/**/**/*_test.go`
- `cmd/agent-ledger/**/**/*_test.go`
- `testdata/**`
- `tests/**`
- `Makefile` only for test targets
- `.github/workflows/**` only if coordinated with Task 011

**Requirements:**

Add unit and integration coverage for:

- Path normalization, symlinks, Unicode NFC.
- Project fingerprinting.
- Assignment allow/forbid matching.
- Conflict policies.
- Event schema and ID shapes.
- Verify JSON output and exit codes.
- Privacy defaults and no full diff persistence.
- Migration idempotency.
- Concurrent claims under disjoint paths, `warn`, and `exclusive`.
- Stale heartbeat recovery and `gc` orphan marking.
- Missed claim adoption.
- Git worktree pointer discovery.
- Separate clone project fingerprint behavior.
- Exported summary verification in a clean checkout/temp project.

**Acceptance criteria:**

- `go test ./...` passes from a clean checkout.
- `go test -race ./...` passes or narrowly justified exclusions exist.
- Tests are temp-dir isolated and deterministic.
- Tests assert stable verify and summary JSON schemas.
- Fake secret/token fixtures prove default persistence is privacy-safe.

**Suggested commands:**

```bash
go test ./...
go test -race ./...
make check
```

**Completion commit:** `Add Phase 1 kernel test coverage`

---

### Task 011: Packaging basics

**Agent:** `worker`

**Depends on:** Task 001. Prefer after Task 009.

**Allowed files/directories:**

- `Makefile`
- `README.md`
- `docs/**`
- `.github/workflows/**`
- `.goreleaser.yaml` or equivalent release config
- `scripts/**`
- `cmd/agent-ledger/**` only for version metadata
- `internal/version/**`

**Requirements:**

- `--version` includes version, commit, and build date when injected by build flags.
- Add local binary build and release archive prep for macOS arm64/amd64 and Linux arm64/amd64.
- Prefer static-friendly build config. If CGO is required, document why.
- `make check` runs formatting, tests, and build verification.
- Add CI workflow with formatting/checks, `go test ./...`, and `go build ./cmd/agent-ledger`, with no secrets.
- Add packaging docs for static binary archive usage and `agent-ledger doctor`.
- Do not claim Phase 2+ adapters are available.

**Acceptance criteria:**

- `make build` produces a local binary.
- `make check` passes.
- Release archive prep can run locally without publishing.
- CI workflow requires no secrets or unavailable services.
- Version metadata injection is validated.

**Suggested commands:**

```bash
make check
make build
./bin/agent-ledger --version
go test ./...
go build ./cmd/agent-ledger
```

**Completion commit:** `Add Phase 1 packaging basics`

---

### Task 012: Final Phase 1 review

**Agent:** `wave-review-orchestrator`

**Depends on:** Tasks 009, 010, and 011 passing `gate-reviewer`.

**Review scope:** Complete Phase 1 kernel slice.

**Required checks:**

- Phase 1 deliverables from `SPEC.md` section 32 are implemented.
- Acceptance scenarios pass:
  1. Two local agents can claim and record disjoint files.
  2. Overlapping claims produce deterministic conflict output.
  3. `verify --json` reports unclaimed and forbidden changes.
  4. No full diff content is stored by default.
- All Phase 1 commands exist and post-MVP `exec` is intentionally excluded.
- `verify --json` matches the stable contract and exit codes.
- Summary verification works without local XDG state.
- Privacy and audit behavior satisfy spec sections 11, 12, 17, 20.1, and 29.
- Packaging docs do not claim unavailable adapter support.

**Required evidence:**

```bash
go test ./...
go test -race ./...
make check
go build ./cmd/agent-ledger
```

Also require representative end-to-end CLI flow and clean-summary verification evidence.

## Recommended execution order

1. Task 001.
2. Tasks 002 and 003, coordinated on shared interfaces.
3. Task 004 wave review.
4. Tasks 005, 006, and 007, coordinated on storage/domain interfaces.
5. Task 008 wave review.
6. Task 009.
7. Tasks 010 and 011.
8. Task 012 final Phase 1 review.
