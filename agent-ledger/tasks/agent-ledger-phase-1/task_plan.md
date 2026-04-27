# Agent Ledger Phase 1: Task Plan

## Iron Law

- **Question:** Execute the Phase 1 kernel slice of Agent Ledger as defined in `SPEC.md` §32 Phase 1.
- **Assumed answer:** Implement the 12-task, 3-wave decomposition in `tasks/phase-1-task-packet.md`, layered with the tech-choice addendum below (dp-002 through dp-008), gated per task by `gate-reviewer` and per wave by `wave-review-orchestrator`.

## Source of truth for worker decomposition

The canonical worker decomposition is `tasks/phase-1-task-packet.md`. This plan does not restate the 12 tasks. It references them and overlays the cross-cutting decisions every task must honor.

INIT decision dp-001 selected **opt-c (amend incrementally)**: keep the existing packet, layer per-task addenda for tech choices instead of regenerating.

## Phases

Each phase corresponds to a wave in `tasks/phase-1-task-packet.md`.

### Phase 1: Foundation (Wave 1)

Goal: Land the Go scaffold, project/storage resolution, path normalization, SQLite migrations, event store, and audit mirror so later waves have a stable substrate.

- [ ] **Task 001**: Go CLI scaffold (`worker`, see packet §Wave 1).
- [ ] **Task 002**: Project discovery, config, storage resolution, path normalization (`worker`).
- [ ] **Task 003**: SQLite storage, migrations, events, audit mirror (`worker`).
- [ ] **Gate per task**: `gate-reviewer` on each of 001, 002, 003 before marking complete.
- [ ] **Wave gate (Task 004)**: `wave-review-orchestrator` after 001+002+003 pass `gate-reviewer`.

Wave-1 success criteria: `go test ./...` green, `go build ./cmd/agent-ledger` green, `agent-ledger init` and `agent-ledger migrate` create the spec-mandated layout in a temp dir, `agent-ledger doctor --json` reports project identity and ledger path, audit JSONL writes are privacy-safe.

### Phase 2: Command workflows (Wave 2)

Goal: Implement the operational commands that turn the Wave 1 substrate into a usable kernel.

- [ ] **Task 005**: Identity, assignment, claim lifecycle, conflicts, status (`worker`).
- [ ] **Task 006**: Change recording, validation recording, adoption, summaries (`worker`).
- [ ] **Task 007**: Operational commands (`migrate`, `gc`, `doctor`) (`worker`).
- [ ] **Gate per task**: `gate-reviewer` on each of 005, 006, 007 before marking complete.
- [ ] **Wave gate (Task 008)**: `wave-review-orchestrator` after 005+006+007 pass `gate-reviewer`.

Wave-2 success criteria: end-to-end local flow (assign → claim → record → close → status), warn-overlap creates a deterministic conflict, exclusive-overlap blocks the second intent with exit code 4, scope enforcement rejects forbidden and outside-assignment paths without writing intent events, summaries export privacy-safe, `gc` marks stale intents orphaned without deleting history, `doctor --json` is machine-readable.

If the wave-1 review surfaces oversize concerns about Task 005, split it into 005a (identity + assign) and 005b (claim + conflicts + status) per dp-005's mitigation. Default is to keep it single.

### Phase 3: Verification, tests, packaging (Wave 3)

Goal: Land the stable verify JSON contract, broaden test coverage to SPEC §31 scenarios, and package the binary for the four MVP targets.

- [ ] **Task 009**: Verification contract (`worker`).
- [ ] **Task 010**: Phase 1 test suite (`worker`).
- [ ] **Task 011**: Packaging basics (`worker`).
- [ ] **Gate per task**: `gate-reviewer` on each of 009, 010, 011 before marking complete.
- [ ] **Wave gate (Task 012)**: `wave-review-orchestrator` (final Phase 1 review) after 009+010+011 pass `gate-reviewer`.

Wave-3 success criteria: `verify --json`, `verify --task`, `verify --summary` all match `agent-ledger.verify.v1` schema and exit codes from SPEC §19; `go test ./...` and `go test -race ./...` green from a clean checkout; subprocess integration tests for SPEC §31.2 scenarios run by default in CI without `-short` skip; `make build` produces a binary; goreleaser snapshot produces archives for darwin/linux × arm64/amd64; CI requires no secrets.

## Phase 1 tech choices addendum

Every worker task in `tasks/phase-1-task-packet.md` inherits these decisions. Where a task's "Allowed files/directories" or "Requirements" sections leave a choice open, the answer is here. If a task description appears to contradict this addendum, the addendum wins and the worker should surface the contradiction in their gate-review notes.

### dp-002: Implementation language → **Go** (opt-a)

Honor SPEC §7. No spec amendment. All Phase 1 code lives under a single Go module rooted at the repo root with `cmd/agent-ledger` as the executable entrypoint.

### dp-003: SQLite driver → **modernc.org/sqlite** (opt-a, pure Go)

- Import path: `modernc.org/sqlite` registered as `database/sql` driver name `"sqlite"`.
- Rationale: pure Go enables `CGO_ENABLED=0` static cross-compilation across all four MVP targets (SPEC §28, §30) without per-target C toolchains.
- **Mandatory mitigation in Task 010**: integration tests for SPEC §31.2 #1, #2, #3 (concurrent claims under disjoint, warn, exclusive policies) must run as real subprocesses with the `-race` flag. These tests gate the release.
- Reserve `mattn/go-sqlite3` as a build-tag fallback (`-tags=cgosqlite`) only if the falsification signal trips. Do not import it in Phase 1 unless the signal trips.
- Apply pragmas exactly as SPEC §10 specifies: `journal_mode=WAL`, `synchronous=NORMAL`, `foreign_keys=ON`, `busy_timeout=5000`. Wrap all writes in transactions; retry on `SQLITE_BUSY` until `busy_timeout` expires, then return a storage error (exit code 3).

### dp-004: CLI framework → **spf13/cobra** (opt-a)

- Import only `github.com/spf13/cobra`. Do NOT pull in viper.
- Use persistent flags for cross-command concerns (`--json`, `--agent`).
- Use `cobra.Command.RunE` for all command handlers so error/exit-code mapping flows through a single helper in `internal/cli`.
- Generated `--help` and `--version` satisfy Task 001 acceptance directly. Shell completions are nice-to-have, not blocking.
- Keep dependency hygiene: prefer stdlib for flag parsing helpers cobra does not already cover.

### dp-005: Phase 1 scope → **Full 15-command Phase 1** (opt-a)

- Ship all 15 commands listed in SPEC §18.17 (init, identify, assign, claim, heartbeat, record, close, status, verify, conflicts, adopt, export-summary, gc, migrate, doctor). `exec` is post-MVP and intentionally excluded.
- Do not introduce a "Phase 1.5" preview release.
- If Wave 1 review flags Task 005 as oversized, split into 005a/005b only with explicit approval from the wave-review orchestrator.

### dp-006: Exclusive policy enforcement → **DB state + best-effort OS lock with warning** (opt-c)

- DB state is authoritative for policy decisions per SPEC §28.
- On `claim` with `exclusive` policy, attempt `flock`-based advisory lock at `<ledger-dir>/locks/<path-hash>.lock` using a single cross-platform abstraction in `internal/locks`.
- If the lock cannot be acquired or is unsupported on the platform, surface the result in `verify --json` via the `EXCLUSIVE_LOCK_HELD` finding (already enumerated in SPEC §19.3) at severity `warning`, not `error`. Do not block the claim on lock-acquisition failure when DB serialization holds.
- Cross-clone coordination is explicitly out of scope (SPEC §2 #4).
- Suggested library: `github.com/gofrs/flock`. Worker may substitute a stdlib-only equivalent, but must justify in commit message.

### dp-007: Test strategy → **Layered: unit + targeted subprocess** (opt-c)

- Unit tests cover all pure-Go logic against in-process temp ledger directories.
- Subprocess integration tests, built via `TestMain` that compiles the binary once per test session, cover SPEC §31.2 #1–3 (concurrent claim semantics) and #6–7 (worktree pointer discovery, separate-clone fingerprint).
- Subprocess tests **must not** be gated behind `-short` or a build tag in CI. Task 010 acceptance treats this as a hard gate.
- Run the full suite under `-race` in CI. Document any race-detector exclusions inline.
- Use stdlib `testing` only. No third-party assertion library.

### dp-008: Packaging → **GoReleaser config without publishing** (opt-c)

- Land `.goreleaser.yaml` configured for darwin/linux × arm64/amd64, with `CGO_ENABLED=0`, `-trimpath`, and `-ldflags="-s -w -X .../internal/version.Version=… -X .../internal/version.Commit=… -X .../internal/version.Date=…"`.
- Wire `goreleaser release --snapshot --clean` into CI on every PR for packaging smoke. No secrets, no PAT, no Homebrew tap, no signing keys in Phase 1.
- File a Phase 5 hardening backlog item the moment Task 011 lands: "Add Homebrew tap, checksums signing, and tagged release publishing." Capture in `tasks/agent-ledger-phase-5-backlog.md` (create on demand).
- `make check` runs fmt + vet + `go test ./...` + `go build ./cmd/agent-ledger`. `make build` produces `bin/agent-ledger`.

### Cross-cutting reminders (apply to every task)

- All `*_at` columns store RFC 3339 UTC with `Z` suffix.
- All `*_sha256` columns and `path_hash` store full 64-character lowercase hex (`path_hash` derived from `sha256(realpath-normalized)`).
- Every domain write inserts a domain row, an `events` row, and an audit JSONL line.
- `payload_json` must never carry raw hook input, raw tool payload, command output, env vars, file contents, full diffs, headers, tokens, or secrets.
- IDs use `<type>_<ulid>` shape (`evt_…`, `asg_…`, `int_…`, `chg_…`, `cfl_…`).
- Honor SPEC §17 privacy: `--include-diff` requires `--yes` in non-interactive contexts; full diffs go to `blobs/sha256/ab/abc…`.
- `SPEC.md` is read-only for implementation tasks.

## Per-wave gating protocol

For every wave:

1. Each worker task lands with a completion commit on its own branch or PR.
2. `gate-reviewer` runs against the completed task before the orchestrator marks it complete. Findings either close out or route back to the worker.
3. After all worker tasks in the wave have passed `gate-reviewer`, the main orchestrator dispatches `wave-review-orchestrator` for the wave-review task (004, 008, 012).
4. `wave-review-orchestrator` does not auto-execute fixes. It returns findings and, if needed, a remediation packet routed to `worker` (implementation defects), `task-generator` (sequencing/decomposition defects), or `oracle` (architectural concerns).
5. The main orchestrator decides whether to dispatch remediation or advance to the next wave.

## Files this plan governs

- Source of truth for tasks: `tasks/phase-1-task-packet.md` (read-only for workers; addendum here overrides where ambiguous).
- This plan: `tasks/agent-ledger-phase-1/task_plan.md`.
- Findings: `tasks/agent-ledger-phase-1/findings.md`.
- Progress log: `tasks/agent-ledger-phase-1/progress.md`.
