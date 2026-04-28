Completed Wave B remediation for PR #8.

Changed:
- `adapters/shared/session-bootstrap.sh`
  - PR detection now uses commit-independent `gh pr view` lookup.
  - Branch detection now resolves unborn branches.
  - Removed dead no-op in the explicit task source case.
  - Added `--if-absent` to bootstrap assignment.
  - Shell export now emits `AGENT_LEDGER_AUTO_ASSIGNED=0` for non-auto sources.
- `adapters/tests/run.sh`
  - Extended the stub to note `--if-absent` usage.
  - Added tests for PR detection success, PR fallthrough, unborn branches, replay idempotency, and shell export behavior.

Validation:
- `bash -n adapters/shared/session-bootstrap.sh adapters/shared/marker.sh adapters/pi/install.sh`
- `bash adapters/tests/run.sh`
- `go test ./internal/domain ./internal/commands`
- `make check`
