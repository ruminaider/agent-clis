# Changelog

All notable changes to `agent-ledger` are recorded here. The format
follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and
the project aims to follow [Semantic Versioning](https://semver.org/)
once a stable release is tagged. Phase 1 is pre-1.0; the
`agent-ledger.verify.v1` and `agent-ledger-summary.v1` schemas are
covered by their version suffix and will follow semver independently
of the binary version.

## [Unreleased]

### Phase 1 kernel slice

The first complete kernel implementation. Harness-neutral: pi, Claude
Code, and Babysitter adapters arrive in later phases.

#### Added

- Go static CLI `agent-ledger` with all 15 Phase 1 subcommands:
  `init`, `identify`, `assign`, `claim`, `heartbeat`, `record`,
  `close`, `status`, `verify`, `conflicts`, `adopt`,
  `export-summary`, `gc`, `migrate`, `doctor`. Built with
  `spf13/cobra` and `CGO_ENABLED=0`.
- Pure-Go SQLite storage (`modernc.org/sqlite`) with WAL,
  `synchronous=NORMAL`, `foreign_keys=ON`, and `busy_timeout=5000`.
  Idempotent migrations cover every Phase 1 table from SPEC §11.
- Append-only JSONL audit mirror with daily UTC rotation. Every
  domain write produces a domain row, an `events` row, and an audit
  line in one transaction (audit best-effort post-commit per SPEC §28).
- ULID-based identifiers (`<type>_<ulid>`), RFC3339 UTC timestamps,
  64-character lowercase hex hashes.
- Privacy-safe persistence by default. `internal/privacy.AssertSafe`
  rejects secret-shaped values (AWS keys, GitHub PATs, OpenAI keys,
  bearer tokens, env-dump shapes) for `reason` inputs at the CLI
  layer, audit JSONL, and exported summaries. Full diffs are stored
  only when `--include-diff --yes` is passed and live in
  content-addressed blobs, not the database.
- `agent-ledger.verify.v1` JSON contract per SPEC §19.2/§19.3:
  flat summary, scalar `path`, `suggested_recovery`, exit codes 0/1
  (pass/fail), 2 (config), 3 (storage), 4 (needs-decision/conflict).
  All 14 finding codes from SPEC §19.3 are emitted.
- `agent-ledger-summary.v1` exported summary schema, with portable
  `path_hash` (NFC sha256 of project-relative display path with
  forward slashes). Cross-checkout summary verification works against
  any working tree without local ledger state.
- `agent-ledger.doctor.v1` health output covering project identity,
  ledger directory writability, git detection, pointer file validity,
  policy file validity, lock support, adapter env vars, SQLite
  pragmas, and migrations.
- `gc --stale-after <duration>` orphans active intents whose last
  heartbeat is older than the cutoff. Idempotent. History is never
  deleted.
- Project fingerprinting: 24-character lowercase hex SHA-256 prefix
  of git common-dir realpath plus origin metadata. Worktrees of one
  repository share a fingerprint; separate clones get distinct
  fingerprints.
- XDG-aware ledger resolution: `$AGENT_LEDGER_DIR` overrides the
  default `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger/repos/<slug>-<fingerprint>/`.
  Local `.agent-ledger.toml` and `.agent-ledger-policy.toml` pointer
  files are honored.
- Exit code constants per SPEC §19.1 in `internal/cli/exitcodes.go`.
  Codes 0-5 follow the spec exactly; 6-12 are documented internal
  extensions outside the public contract.
- Best-effort advisory file locks via `gofrs/flock` for exclusive
  claim semantics. Lock state is reported by `verify` as
  `EXCLUSIVE_LOCK_HELD` when relevant.
- `make build` injects `Version`, `Commit`, and `BuildDate` via
  `-ldflags -X` from `git describe`, `git rev-parse --short HEAD`,
  and the build timestamp.
- GoReleaser snapshot config for darwin/linux × arm64/amd64 archives.
  Local-only (no publishing, no secrets). Homebrew tap and signing
  deferred to Phase 5.
- GitHub Actions workflows at the monorepo root, name-prefixed
  `agent-ledger-*` and `paths:`-scoped to `agent-ledger/`:
  `agent-ledger-ci.yml` runs `make check`, a 4-target cross-build
  matrix, and a `go test -race` job;
  `agent-ledger-release-snapshot.yml` exercises GoReleaser on every
  pull request without publishing;
  `agent-ledger-release.yml` publishes a real GitHub Release on `v*`
  tag push using the auto-provided `GITHUB_TOKEN`. No
  user-configured secrets are required at any stage.
- Subprocess integration test suite under `tests/integration/`
  covering concurrent claims (disjoint, warn, exclusive), secret
  fixture leak scans, stable verify and summary JSON schemas,
  separate-clone fingerprint distinctness, worktree pointer
  discovery, and cross-root summary portability.

#### Documentation

- README with concentric-circles structure (what / quick start /
  configuration / usage / development / architecture).
- `docs/packaging.md` for build, archive, and CI details.
- `docs/walkthrough.md` for the full lifecycle end-to-end.
- `docs/exit-codes.md` listing every exit code emitted by the CLI.
- `docs/finding-codes.md` listing every `verify` finding code.
- `SPEC.md` is the authoritative design spec for the kernel and the
  later adapter phases.

#### Known limitations

- Adapters for pi, Claude Code, and Babysitter are not shipped yet.
  Use the CLI directly. See SPEC §32 for the post-Phase-1 roadmap.
- The local SQLite store retains plain-text `reason` fields for
  debugging. `AssertSafe` is pattern-based, not cryptographic; agents
  must not embed secrets in reason fields regardless of the guard.
  A scrubbing pass is on the Phase 5 backlog.
- The `claim` command runs `conflicts.Resolve` outside the intent
  insert transaction, so two concurrent claims on the same path under
  `exclusive` can both pass the overlap check before either writes.
  Exclusive claims serialized through one orchestrator are unaffected;
  the bug surfaces only for true concurrent races. Tracked for v0.1.x;
  the fix moves conflict detection inside a single `BEGIN IMMEDIATE`
  transaction with the intent insert. The integration test
  `TestConcurrent_ExclusivePolicy` is skipped with a TODO until then.
- Release archives are unsigned. Verify downloads against the
  published `*_checksums.txt` file. A Homebrew tap with signed
  binaries is a Phase 5 deliverable.

[Unreleased]: https://github.com/ruminaider/agent-clis/compare/main...HEAD
