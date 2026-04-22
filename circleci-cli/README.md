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
circleci-cli logs [--target <target>] [--branch <branch>] [--pipeline-id <id> | --workflow-id <id> | --job-number <n> | --job-name <name>] [--project-slug <slug>] [--failed-only] [--tail <n>] [--format json|text]
circleci-cli flaky-tests [--target <target>] [--branch <branch>] [--window <n>] [--min-failures <n>] [--project-slug <slug>] [--format json|text]
circleci-cli config validate [--path .circleci/config.yml] [--format json|text]
```

## What is implemented now

Current supported capabilities:
- `doctor`: validates auth, project slug resolution, and whether the official `circleci` binary is installed
- `status`: finds the most relevant recent pipeline for a target and summarizes workflows, jobs, counts, and direct links
- `pipeline`: inspects a specific pipeline ID with the same normalized output as `status`
- `logs`: resolves a job by number, workflow, pipeline, or job name, then returns root-cause-oriented failure logs, test failures, and artifacts
- `flaky-tests`: fetches and normalizes CircleCI flaky test insights for a target
- `config validate`: delegates to the official `circleci config validate` command when available

## Auth and configuration

`circleci-cli` resolves authentication and project scope in this order:

Project slug:
1. `--project-slug`
2. `PI_CIRCLECI_PROJECT_SLUG`
3. `targets.yml` default mapping

Token:
1. `CIRCLECI_TOKEN`
2. `PI_CIRCLECI_TOKEN`

The `doctor` command validates the resolved token and project slug.

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

Exit codes:
- `0`: command succeeded
- `1`: auth, HTTP, or operational failure
- `2`: argument or usage error
- `3`: command succeeded, but `status --fail-on-ci-failure` found a failed primary job

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
