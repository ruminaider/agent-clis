---
name: circleci
description: Use when the user wants to inspect CircleCI through `circleci-cli`: check pipeline status, pull failure logs, investigate flaky tests, validate `.circleci/config.yml`, or diagnose auth and project-slug setup. Also use when the user explicitly asks for `circleci-cli`. Do NOT use for the official `circleci` binary, CircleCI's web UI, or write operations (rerun, cancel, approve); this wrapper is read-only.
---

# CircleCI CLI

## Overview
`circleci-cli` is a JSON-first, read-only wrapper over CircleCI's authenticated APIs, with thin delegation to the official `circleci` binary for config validation. Prefer this wrapper over the official binary for every inspection task, and always scope queries by `--target`.

## Core Philosophy
**Use `circleci-cli`, not `circleci`.** The official binary is trained into most agents. This wrapper returns structured JSON, normalizes failure output, and understands per-target workflows. Only `config validate` delegates to the official binary, and only when `doctor` reports it installed.

**Always scope by `--target`.** Targets (`auth`, `care`, `tonic`, `agent-core`, `all`) map to specific workflows and jobs in `targets.yml`. Without `--target`, resolution may pick an unrelated pipeline on the default slug. Omit `--target` only when the user has explicitly asked about the whole project.

**Run `doctor` before the first real call.** It validates the token, resolves the project slug, and reports whether the official `circleci` binary is available. Most "nothing works" failures are a missing or misnamed token.

**This wrapper is read-only.** No rerun, cancel, approve, or artifact-download commands exist. Do not invent them. If the user asks for a write action, point them at the CircleCI UI.

## Domain mechanics

1. **Doctor.** `circleci-cli doctor [--project-slug <slug>]`. Run first. Confirms auth, slug, and official-binary presence. `token_present` reflects pure presence (a non-empty token was resolved); `auth_valid` reflects API acceptance. An invalid token shows `token_present=true`, `auth_valid=false`, with the error in `token_error`.
2. **Status.** `circleci-cli status --target <t> [--branch <b>] [--commit <sha>] [--fail-on-ci-failure]`. Selects the latest matching pipeline by `created_at` and summarizes workflows, jobs, counts, and links. Failure-aware ranking applies only as a tiebreaker when timestamps match. `--fail-on-ci-failure` makes the command exit 3 when the resolved pipeline has ANY failed matched job (keyed off `status_counts.failed > 0`, not just the display-oriented `primary_job`). Use it inside scripts that must branch on CI health.
3. **Pipeline.** `circleci-cli pipeline <pipeline-id> --target <t>`. Same normalized shape as `status`, scoped to an explicit pipeline id from a prior `status` call or a CircleCI URL.
4. **Logs.** `circleci-cli logs --target <t> [--branch <b>] [--failed-only] [--tail <n>] [selector]`. Selector rules (enforced by the CLI; conflicts are rejected with a `USAGE_ERROR` before any API call):
   - `--job-number <n>` is standalone; cannot be combined with any other selector.
   - `--workflow-id <id>` and `--pipeline-id <id>` are mutually exclusive.
   - `--job-name <name>` requires exactly one of `--workflow-id` or `--pipeline-id`.
   - Omitting all selectors falls back to the default target-based resolver.
   Output fetching is lazy: only selected sections incur HTTP calls. Pass `--failed-only` for root-cause output and `--tail` to cap lines per step. When using `--job-name`, also pass `--target` (and usually `--branch`) so the resolver can find the right workflow. Expected-empty responses (jobs with no tests or artifacts) return `[]`; real fetch failures surface as structured errors.
5. **Flaky tests.** `circleci-cli flaky-tests --target <t> [--branch <b>] [--window <n>] [--min-failures <n>]`. Returns normalized flaky-test insights. `--window` controls recency; `--min-failures` filters the tail.
6. **Config validate.** `circleci-cli config validate [--path .circleci/config.yml]`. Delegates to the official `circleci` binary. If `doctor` says the binary is missing, install it before calling this. The CLI propagates the delegated subprocess exit code on failure while preserving the JSON response envelope.

*Judgment:* If the user gives a CircleCI URL, extract the pipeline id from it and use `pipeline` rather than guessing at `status` filters. If the user gives a branch only, use `status --target <t> --branch <b>`, then drill down with `logs` using the returned pipeline or workflow id.

*Judgment:* For "why did CI fail?" requests, the canonical chain is `status` → note the failed workflow or job → `logs --workflow-id <id> --failed-only --tail 200`. Do not start at `logs` without an id.

## Auth and configuration

**Token precedence:**
1. `CIRCLECI_TOKEN`
2. `PI_CIRCLECI_TOKEN`

Do not use `CIRCLECI_API_TOKEN`; that is the official binary's env var and this wrapper ignores it.

**Project slug precedence:**
1. `--project-slug <slug>`
2. `PI_CIRCLECI_PROJECT_SLUG`
3. `default_project_slug` from `src/circleci_cli/targets.yml`

## Output contract

Success (stdout, single JSON object):
```json
{ "command": "status", "target": "tonic", "summary": "...", "data": { ... }, "links": { ... }, "meta": { ... } }
```

Error (stderr, single JSON object):
```json
{ "error": { "code": "AUTH_ERROR", "message": "...", "details": { ... } } }
```

Common codes: `AUTH_ERROR`, `USAGE_ERROR` (malformed invocations and conflicting `logs` selectors; argparse failures are converted to this envelope rather than emitted as plain text), `NOT_FOUND`, `CLI_ERROR`.

**Exit codes:**
- `0`: success
- `1`: auth, HTTP, or operational failure
- `2`: argument or usage error
- `3`: command succeeded but `status --fail-on-ci-failure` found at least one failed matched job
- `config validate` propagates the delegated tool's exit code when validation fails

Errors go to stderr. When piping through `jq`, capture both streams or read stderr separately.

## Not implemented (do not invent)
- Rerun, cancel, approve, or any write action
- Standalone artifact download (artifacts surface inside `logs` output)
- Cross-invocation caching
- Rich text formatters beyond `--format text`

## Common mistakes
- Calling the official `circleci` binary directly for status or logs. Use `circleci-cli`.
- Omitting `--target`. Resolution then depends on the default slug and recent activity, which is rarely what the user meant.
- Starting at `logs` without an id. Run `status` first, then pass the returned workflow or pipeline id.
- Guessing `--job` instead of `--job-name`, or combining conflicting selectors. The CLI now rejects invalid combinations with a `USAGE_ERROR` (exit 2) before any API call; follow the selector rules above rather than relying on silent precedence.
- Setting `CIRCLECI_API_TOKEN` instead of `CIRCLECI_TOKEN`.
- Treating exit 0 as "CI green." Without `--fail-on-ci-failure`, a failed pipeline still exits 0. Inspect `data.pipeline.status_counts` or add the flag.
- Confusing `token_present` with `auth_valid` in `doctor` output. A revoked or typo'd token has `token_present=true`, `auth_valid=false`; key on `auth_valid` to decide whether auth works.
- Parsing error output from stdout. Errors are on stderr.
- Inventing rerun or cancel commands. The wrapper is read-only by design.
