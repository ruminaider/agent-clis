# CircleCI CLI

`circleci-cli` is a JSON-first CircleCI companion for AI agents. It is a read-only wrapper over CircleCI's authenticated APIs, with optional delegation to the official `circleci` binary for config validation and environment diagnostics.

This package exists because pi does not use MCP directly. Instead, it needs a small, composable CLI with stable output.

## Quick start

```bash
cd /Users/albert/Repositories/agent-clis/circleci-cli
python3 -m venv .venv
source .venv/bin/activate
pip install -e .[dev]

export CIRCLECI_TOKEN=your-token
circleci-cli doctor
circleci-cli status --target tonic
```

## Command surface

```bash
circleci-cli doctor
circleci-cli status [--target <target>] [--branch <branch>] [--commit <sha>] [--project-slug <slug>] [--format json|text] [--fail-on-ci-failure]
circleci-cli pipeline <pipeline-id> [--target <target>] [--project-slug <slug>] [--format json|text]
circleci-cli logs [--target <target>] [--branch <branch>] [selector] [--project-slug <slug>] [--failed-only] [--tail <n>] [--format json|text]
# selector rules (conflicts are rejected with USAGE_ERROR before any API call):
#   --job-number <n>                         standalone
#   --workflow-id <id>                       mutually exclusive with --pipeline-id
#   --pipeline-id <id>                       mutually exclusive with --workflow-id
#   --job-name <name> --workflow-id <id>     (or --pipeline-id)
# omitting all selectors falls back to the default target-based resolver
circleci-cli flaky-tests [--target <target>] [--branch <branch>] [--window <n>] [--min-failures <n>] [--project-slug <slug>] [--format json|text]
circleci-cli config validate [--path .circleci/config.yml] [--format json|text]
```

## What is implemented now

Current supported capabilities:
- `doctor`: validates auth, project slug resolution, and whether the official `circleci` binary is installed
- `status`: selects the latest matching pipeline by `created_at` and summarizes workflows, jobs, counts, and direct links. Failure-aware ranking is a tiebreaker only when timestamps match.
- `pipeline`: inspects a specific pipeline ID with the same normalized output as `status`
- `logs`: resolves a job by number, workflow, pipeline, or job name, then returns root-cause-oriented failure logs, test failures, and artifacts. Output fetching is lazy: only selected sections incur HTTP calls. Real fetch failures (auth, 5xx, network) surface as errors; expected-empty responses (404 for jobs with no tests or artifacts) return `[]`.
- `flaky-tests`: fetches and normalizes CircleCI flaky test insights for a target
- `config validate`: delegates to the official `circleci config validate` command. The CLI propagates the delegated exit code on failure while preserving the JSON response envelope.

## Auth and configuration

`circleci-cli` resolves authentication and project scope in this order:

Project slug:
1. `--project-slug`
2. `PI_CIRCLECI_PROJECT_SLUG`
3. `targets.yml` default mapping

Token:
1. `CIRCLECI_TOKEN`
2. `PI_CIRCLECI_TOKEN`

The `doctor` command validates the resolved token and project slug. Two fields distinguish presence from validity:
- `token_present`: `true` whenever a non-empty token was resolved from the precedence chain
- `auth_valid`: `true` only if the token was accepted by the CircleCI API

An invalid token keeps `token_present=true` and sets `auth_valid=false`, with the underlying error surfaced in `token_error`.

## Output contract

Successful commands emit a single JSON object to stdout by default:

```json
{
  "command": "status",
  "target": "tonic",
  "summary": "Pipeline 44113 failed in tonic-tests",
  "data": {
    "pipeline": {
      "id": "c53cfbfa-172e-41e9-8aec-53799155aa28",
      "number": 44113,
      "state": "running"
    }
  },
  "links": {},
  "meta": {}
}
```

Errors emit structured JSON to stderr:

```json
{
  "error": {
    "code": "AUTH_ERROR",
    "message": "Missing CircleCI token",
    "details": {}
  }
}
```

Error codes include `AUTH_ERROR`, `USAGE_ERROR` (malformed invocations, conflicting `logs` selectors; argparse failures are converted to this envelope rather than emitted as plain text), `NOT_FOUND`, and generic `CLI_ERROR`.

Exit codes:
- `0`: command succeeded
- `1`: auth, HTTP, or operational failure
- `2`: argument or usage error
- `3`: command succeeded, but `status --fail-on-ci-failure` found at least one failed matched job in the resolved pipeline (keyed off `status_counts.failed > 0`)
- delegated tools (`config validate`) propagate the underlying tool's exit code when validation fails

## Development

```bash
pip install -e .[dev]
pytest
circleci-cli --help
```

## Repository structure

```text
circleci-cli/
├── install.sh
├── pyproject.toml
├── README.md
├── src/circleci_cli/
│   ├── cli.py
│   ├── auth.py
│   ├── client.py
│   ├── errors.py
│   ├── formatters.py
│   ├── models.py
│   ├── official_cli.py
│   ├── project_resolver.py
│   ├── targets.yml
│   └── services/
└── tests/
```

## Future work deliberately deferred

These are intentionally deferred, not forgotten:
- cross-invocation caching: correctness risk for fast-changing CI state outweighs the performance gain
- separate artifact inspection command: `logs` already surfaces artifacts, and a standalone command would add binary and MIME complexity
- rerun or cancel commands: these are write operations and need separate safety design, confirmation behavior, and audit expectations
- rich text rendering: the CLI is JSON-first, and a heavy text formatter would double maintenance for every schema change
