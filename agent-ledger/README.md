# agent-ledger

Local coordination kernel for agentic coding workflows. `agent-ledger`
records which agent was assigned a task, which files the agent intended
to edit, which files actually changed, why, and whether those changes
stayed within scope.

The kernel is harness-neutral. Phase 2 and beyond will add adapters for
pi, Claude Code, Babysitter, and generic agent harnesses around the same
CLI, storage model, and verification contract. Those adapters are not
yet available.

## Status

Phase 1 (kernel slice) is under active development. This repository
holds the Go CLI, SQLite-backed storage, JSONL audit mirror, and the
`agent-ledger.verify.v1` contract. See `SPEC.md` and
`tasks/phase-1-task-packet.md` for the roadmap. Adapters for pi,
Claude Code, and Babysitter are planned for later phases and are not
shipped here.

## Quick start

### Prebuilt binary

Phase 1 ships unsigned snapshot archives for darwin/arm64, darwin/amd64,
linux/arm64, and linux/amd64. Download an archive from your local
snapshot build (see `docs/packaging.md`), extract it, then run:

```bash
./agent-ledger --version
./agent-ledger doctor
./agent-ledger --help
```

A signed Homebrew tap is planned for Phase 5.

### Build from source

Requires Go 1.22 or newer. Builds are CGO-free.

```bash
git clone https://github.com/ruminaider/agent-clis.git
cd agent-clis/agent-ledger
make build
./bin/agent-ledger --version
```

`--version` prints `agent-ledger version <Version> commit <Commit> built <BuildDate>`,
populated from `git describe`, `git rev-parse --short HEAD`, and the
build timestamp via `-ldflags -X`. When none of those are injected
(`go run ./cmd/agent-ledger`), the binary falls back to `0.0.0-dev`.

## Configuration

`agent-ledger` is configured by environment variables and an optional
project pointer file. Defaults follow the XDG Base Directory spec.

| Variable           | Default                              | Purpose |
| ------------------ | ------------------------------------ | ------- |
| `AGENT_LEDGER_DIR` | `${XDG_STATE_HOME:-$HOME/.local/state}/agent-ledger` | Override the ledger root directory. |
| `XDG_STATE_HOME`   | `$HOME/.local/state`                 | Standard XDG override for ledger storage (per SPEC §8). |

Within the ledger root, each project's data lives under a per-repo
subdirectory keyed by slug and fingerprint:

```text
$XDG_STATE_HOME/agent-ledger/repos/<project-slug>-<project-fingerprint>/
```

The `<project-slug>` is sanitized from the project id or git origin, and
`<project-fingerprint>` is the 24-character SHA-256 prefix described in
SPEC §8.1. Setting `AGENT_LEDGER_DIR` overrides the entire ledger root
(the `repos/<slug>-<fingerprint>/` layout still applies beneath it).

> Note: `XDG_CONFIG_HOME` is not consulted by `agent-ledger`; ledger
> data follows `XDG_STATE_HOME` exclusively.

### Project pointer

A repository can pin its ledger location by committing a small TOML
file (`.agent-ledger.toml`) at the project root:

```toml
version = 1
project_id = "agent-clis/agent-ledger"
ledger_dir = "/abs/path/to/ledger"   # optional; defaults apply if omitted
```

Run `agent-ledger init` to create one interactively.

### Doctor

Use `agent-ledger doctor` to verify the install end-to-end. It prints
the effective ledger directory, schema version, and whether the project
pointer resolves cleanly. Exit code 0 means the kernel can read and
write its ledger.

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

Phase 2+ adapters (pi, Claude Code, Babysitter) are planned but not yet
shipped. Use the CLI directly for now.

## Development

Requires Go 1.22 or newer. Build is CGO-free.

```bash
make fmt              # gofmt -w .
make vet              # go vet ./...
make test             # go test ./...
make build            # bin/agent-ledger with version metadata
make check            # fmt-check + vet + test + build
make test-integration # subprocess suite under tests/
make coverage         # bin/coverage.out + bin/coverage.html
make release-snapshot # local goreleaser snapshot under dist/
```

`make release-snapshot` produces tar.gz archives for the four supported
targets plus a SHA256 checksums file. It runs entirely locally and
needs no secrets. See `docs/packaging.md` for details.

## Architecture

The Phase 1 kernel slice is a single Go binary. Its layout:

```text
cmd/agent-ledger/   # Binary entrypoint; thin main()
internal/cli/       # Cobra wiring, exit codes, error envelope
internal/version/   # Build version metadata (Version, Commit, BuildDate)
internal/config/    # Env and pointer file resolution
internal/paths/     # XDG-aware directory helpers
internal/storage/   # SQLite (pure-Go modernc) + JSONL audit mirror
internal/verify/    # agent-ledger.verify.v1 contract
SPEC.md             # Authoritative design spec
docs/packaging.md   # Build, archive, and CI details
tasks/              # Phase plan, task packets, review notes
```

The kernel owns coordination state. It does not run agents, watch
filesystems, or talk to the network. Adapters in later phases will sit
above this CLI and translate harness events into `assign`, `claim`,
`record`, `close`, and `verify` calls.
