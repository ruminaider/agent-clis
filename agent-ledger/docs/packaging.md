# Packaging

This document describes how `agent-ledger` is built, archived, and
distributed in Phase 1. Phase 5 will introduce a Homebrew tap and signed
release artifacts; until then, the project produces unsigned snapshot
archives that anyone can build locally without secrets.

## Supported platforms

Phase 1 builds and tests on the following targets:

| OS     | Architecture | Notes                          |
| ------ | ------------ | ------------------------------ |
| darwin | arm64        | Apple Silicon                  |
| darwin | amd64        | Intel Macs                     |
| linux  | arm64        | aarch64 (servers, Raspberry Pi) |
| linux  | amd64        | x86_64 (most servers, CI)       |

Windows is intentionally out of scope for Phase 1 (see `SPEC.md` §32).

All builds use `CGO_ENABLED=0`. The Phase 1 SQLite driver is the
pure-Go modernc port, so the resulting binaries are statically linked
and have no shared-library dependencies beyond libc.

## Version metadata

`agent-ledger --version` prints a single line in this format:

```text
agent-ledger version <Version> commit <Commit> built <BuildDate>
```

The three fields are populated at link time via `-ldflags -X`:

- `Version`: `git describe --tags --always --dirty`, or `dev` if git is unavailable.
- `Commit`: `git rev-parse --short HEAD`, or `unknown`.
- `BuildDate`: ISO 8601 UTC timestamp of the build.

When none of those flags are injected (for example, `go run ./cmd/agent-ledger`),
the binary falls back to `0.0.0-dev` / `unknown` / `unknown`.

## Building from source

```bash
make build       # bin/agent-ledger with version metadata
./bin/agent-ledger --version
```

`make build` calls `go build -trimpath -ldflags ...` with the values
described above.

## Local snapshot release

The Phase 1 release flow runs goreleaser in snapshot mode. It produces:

- One `tar.gz` archive per target under `dist/`.
- A `*_checksums.txt` file with SHA256 hashes for every archive.
- No GitHub release, no Homebrew formula, no signing.

```bash
make release-snapshot      # invokes scripts/release-snapshot.sh
# or, equivalently:
goreleaser release --snapshot --clean --skip=publish
```

If goreleaser is not installed, see `scripts/release-snapshot.sh` for
install instructions (`go install github.com/goreleaser/goreleaser/v2@latest`
or `brew install goreleaser`).

## Tagged releases

Real releases publish unsigned tar.gz archives for the four supported
targets to GitHub Releases. The flow:

```bash
# From a clean working tree on a merged main:
git tag -a v0.1.0 -m "Phase 1 kernel slice"
git push origin v0.1.0
```

The `release.yml` workflow runs goreleaser without `--snapshot`,
publishes the four archives plus the SHA256 checksums file as the
release assets, and includes a release body templated from
`.goreleaser.yaml`. Users then download from
`https://github.com/ruminaider/agent-clis/releases/tag/v0.1.0`.

Tags shaped like `v0.1.0-rc1` automatically publish as prereleases
(see `release.prerelease: auto` in `.goreleaser.yaml`).

To cut a release locally without pushing a tag, use the
`workflow_dispatch` trigger on `release.yml` from the GitHub Actions
UI.

## CI workflows

Three GitHub Actions workflows live under `.github/workflows/`:

1. `ci.yml`: runs on every push and pull request.
   - `lint-and-test`: `make check` (gofmt, vet, test, build).
   - `cross-build`: matrix build for the four supported targets.
   - `race-test`: `go test -race -count=1 ./...`.
2. `release-snapshot.yml`: runs on every pull request and on manual
   `workflow_dispatch`. Executes `goreleaser release --snapshot --clean --skip=publish`
   and uploads `dist/` as a workflow artifact. Publishes nothing.
3. `release.yml`: runs on `v*` tag pushes and on manual
   `workflow_dispatch`. Executes `goreleaser release --clean` and
   creates a GitHub Release with the four archives and checksum file
   attached.

`ci.yml` and `release-snapshot.yml` reference no secrets. `release.yml`
uses the auto-provided `${{ secrets.GITHUB_TOKEN }}` to create the
release. No user-configured secrets are required at any stage.

## Verifying an installation

After extracting an archive, run:

```bash
./agent-ledger --version
./agent-ledger doctor
```

`doctor` prints environment and storage diagnostics (effective ledger
directory, SQLite schema version, project pointer resolution). It exits
0 when the kernel can read and write its ledger.

## Phase 5 deferrals

The following are explicitly deferred to Phase 5:

- Homebrew tap and `brew install agent-ledger` flow.
- Code signing for darwin (notarization) and Linux (cosign / minisign).
- Auto-generated changelogs in the release body (currently the body
  is templated from `.goreleaser.yaml`'s `release.header` and
  `release.footer`).

Until signing lands, release archives are unsigned. Users should
verify downloads against the published `*_checksums.txt` file.
