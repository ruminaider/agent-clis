# agent-ledger

Local coordination kernel for agentic coding workflows. `agent-ledger`
records which agent was assigned a task, which files the agent intended
to edit, which files actually changed, why, and whether those changes
stayed within scope.

It targets pi, Claude Code, Babysitter, and generic agent harnesses. The
core stays harness-neutral. Harness integrations are adapters around the
same CLI, storage model, and verification contract.

## Status

Phase 1 (kernel slice) is under active development. This repository
currently holds the Go CLI scaffold: every Phase 1 command is registered,
but most still return exit code 3 (not implemented) until later tasks
land. See `SPEC.md` and `tasks/phase-1-task-packet.md` for the roadmap.

## Quick start

```bash
go run ./cmd/agent-ledger --help
go run ./cmd/agent-ledger --version
```

`--help` lists every Phase 1 subcommand. Stubs print a structured
not-implemented envelope on stderr and exit with code 3 so callers can
distinguish "feature missing" from real failures.

## Usage

The Phase 1 command set (see `SPEC.md` §18) is:

```text
init           Initialize ledger storage and optional pointer file
identify       Create or print an agent session identity
assign         Record an orchestrator assignment for a task
claim          Open a worker intent over one or more paths
heartbeat      Renew an active intent
record         Record a change made under an open intent
close          Close an intent with an outcome
status         Show active claims, recent changes, and conflicts
verify         Produce the agent-ledger.verify.v1 contract
conflicts      List or acknowledge coordination conflicts
adopt          Retroactively adopt an unclaimed change
export-summary Export a privacy-safe task summary for CI
gc             Mark stale active intents as orphaned
migrate        Apply schema migrations
doctor         Run environment and storage diagnostics
```

Add `--json` to any command to receive a machine-readable error envelope
on failure (`{status, code, message, details}`). Exit codes are stable
and defined in `SPEC.md` §19.1.

## Development

Requires Go 1.22 or newer. Build is CGO-free until SQLite lands.

```bash
make fmt        # gofmt -w .
make test       # go test ./...
make build      # produces bin/agent-ledger
make check      # fmt-check + vet + test + build
```

### Layout

```text
cmd/agent-ledger/   # Binary entrypoint; thin main()
internal/cli/       # Cobra wiring, exit codes, error envelope
internal/version/   # Build version constant (overridable via -ldflags)
SPEC.md             # Authoritative design spec
tasks/              # Phase plan, task packets, review notes
```

Subsequent Phase 1 tasks add `internal/config`, `internal/project`,
`internal/paths`, `internal/storage`, and the SQLite-backed ledger.
