# Agent Ledger Phase 1: Progress Log

| Wave | Status | Tasks | Gate-reviewer | Wave review | Notes |
|---|---|---|---|---|---|
| Wave 1: Foundation | complete | 001, 002, 003 | passed (per task) | passed via final wave review | Scaffold, storage resolution, SQLite migrations + audit mirror. |
| Wave 2: Command workflows | complete | 005, 006, 007 | passed (per task) | passed via final wave review | identify/assign/claim/conflicts/status, record/adopt/export-summary, doctor/migrate/gc. |
| Wave 3: Verification, tests, packaging | complete | 009, 010, 011 | passed (per task) | passed via final wave review | verify --json contract, integration test suite, packaging (GoReleaser snapshot, CI). |
| Refining iteration 1 | complete | R-001..R-007 | passed | re-validated | Address spec drift on verify contract, exit codes, summary path_hash, README XDG, privacy reasons, subprocess tests, CI race. |
| Refining iteration 2 | complete | RV2-001..RV2-006 | passed | re-validated | Suggestions and follow-ups from re-validation iter 1. |
| Refining iteration 3 | complete | RV3-001..RV3-003 | passed | not re-validated (cap reached) | Closed regressions introduced by RV2. Final super-reviewer pass over RV3 commits returned clean. |

## Outcome

Phase 1 kernel slice is feature-complete. 25 commits on the working
branch implement SPEC §32 Phase 1: Go static CLI, SQLite-backed
storage, JSONL audit mirror, privacy-safe persistence, the
`agent-ledger.verify.v1` contract, the `agent-ledger-summary.v1`
schema with portable path hashing, GoReleaser snapshot packaging, and
CI with race-detector and snapshot smoke jobs. `make check` passes
and an end-to-end smoke against the snapshot tarball succeeds.

See `CHANGELOG.md` for release notes and `docs/walkthrough.md` for the
canonical lifecycle example.

## Next phase

Phase 2 begins with the pi adapter per SPEC §32. The kernel CLI and
storage contract are stable inputs to that work.
