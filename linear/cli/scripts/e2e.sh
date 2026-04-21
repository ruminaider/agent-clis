#!/usr/bin/env bash
# End-to-end regression test for linear-cli.
#
# Runs the full skill workflow against a real Linear workspace:
# auth check, team resolve, create/update issue, assign + clear assignee,
# add comment, attach file, list/delete comment, delete attachment, cancel issue.
#
# Exits non-zero on any step failure. Pass --team <team> to target a team
# other than the one in LINEAR_DEFAULT_TEAM. Pass --keep to skip cancellation
# of the test issue (useful when debugging a failure).
#
# Linear's MCP has no delete_issue tool; canceled test issues remain in the
# workspace with a "(safe to delete)" marker in the title.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_BIN="$SCRIPT_DIR/../bin/linear.js"

TEAM="${LINEAR_DEFAULT_TEAM:-}"
KEEP_ISSUE=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --team) TEAM="$2"; shift 2 ;;
    --keep) KEEP_ISSUE=1; shift ;;
    -h|--help)
      cat <<'USAGE'
Usage: e2e.sh [--team <team>] [--keep]

Requires a usable auth credential (OAuth or LINEAR_API_KEY) and either
LINEAR_DEFAULT_TEAM or --team <team>. Writes are made to a disposable test
issue that is Canceled at the end unless --keep is set.
USAGE
      exit 0
      ;;
    *) echo "Unknown flag: $1" >&2; exit 2 ;;
  esac
done

if [[ -z "$TEAM" ]]; then
  echo "ERROR: --team <team> required, or set LINEAR_DEFAULT_TEAM" >&2
  exit 2
fi

cli() { node "$CLI_BIN" "$@"; }

# Extract a JSON path from stdin. Fails loudly if the path does not resolve
# to a string, so steps that silently degrade to an error message surface.
jpath() {
  local path="$1"
  python3 -c "
import json, sys
d = json.load(sys.stdin)
cur = d
for part in '$path'.split('.'):
  if isinstance(cur, list):
    cur = cur[int(part)]
  else:
    cur = cur.get(part)
  if cur is None:
    raise SystemExit(f'path \"$path\" resolved to None')
print(cur)
"
}

fail() { echo "FAIL at step: $1" >&2; exit 1; }

STEP=0
step() { STEP=$((STEP + 1)); echo; echo "--- [$STEP] $1 ---"; }

step "auth status"
cli auth status >/tmp/linear-e2e-auth.json || fail "auth status"
authed=$(jpath "authenticated" </tmp/linear-e2e-auth.json)
[[ "$authed" == "True" ]] || fail "not authenticated"

step "resolve team: $TEAM"
cli team get --query "$TEAM" >/tmp/linear-e2e-team.json || fail "team get"
TEAM_ID=$(jpath "team.id" </tmp/linear-e2e-team.json)
echo "team: $TEAM ($TEAM_ID)"

step "create test issue"
TITLE="linear-cli e2e $(date +%Y%m%d-%H%M%S) (safe to delete)"
cli issue save \
  --title "$TITLE" \
  --team "$TEAM" \
  --priority 4 \
  --description "Automated e2e test. Safe to delete." \
  >/tmp/linear-e2e-create.json || fail "issue create"
IID=$(jpath "issue.id" </tmp/linear-e2e-create.json)
echo "issue: $IID"

step "update issue: priority and labels"
cli issue save --id "$IID" --priority 3 >/tmp/linear-e2e-update.json || fail "priority update"

step "assign self, then clear (--clear-assignee)"
cli issue save --id "$IID" --assignee me >/tmp/linear-e2e-assign.json || fail "assign"
ASSIGNEE=$(python3 -c "
import json
i = json.load(open('/tmp/linear-e2e-assign.json'))['issue']
if not isinstance(i, dict): raise SystemExit(f'issue not object: {i}')
a = i.get('assignee')
if not a: raise SystemExit(f'assignee not set: {a}')
print(a if isinstance(a, str) else a.get('displayName') or a.get('name'))
")
echo "assigned to: $ASSIGNEE"

cli issue save --id "$IID" --clear-assignee >/tmp/linear-e2e-clear.json || fail "clear-assignee"
python3 -c "
import json
i = json.load(open('/tmp/linear-e2e-clear.json'))['issue']
if not isinstance(i, dict) or i.get('assignee') is not None:
  raise SystemExit(f'assignee not cleared: {i}')
print('assignee: None (cleared)')
"

step "mutual exclusion check: --assignee + --clear-assignee"
if cli issue save --id "$IID" --assignee me --clear-assignee >/tmp/linear-e2e-mutex.json 2>&1; then
  MUTEX_OK=$(python3 -c "import json; d=json.load(open('/tmp/linear-e2e-mutex.json')); print(not d.get('ok'))")
  [[ "$MUTEX_OK" == "True" ]] || fail "mutex check did not reject"
fi
echo "mutex rejection ok"

step "add comment"
cli comment save --issue-id "$IID" --body "e2e test comment" >/tmp/linear-e2e-comment.json || fail "comment save"
CID=$(jpath "comment.id" </tmp/linear-e2e-comment.json)
echo "comment: $CID"

step "attach file"
echo "e2e payload $(date -Iseconds)" >/tmp/linear-e2e-payload.txt
cli attachment create \
  --issue "$IID" \
  --file /tmp/linear-e2e-payload.txt \
  --title "e2e attachment" \
  >/tmp/linear-e2e-attach.json || fail "attachment create"
AID=$(jpath "attachment.id" </tmp/linear-e2e-attach.json)
echo "attachment: $AID"

step "list comments (expect >=1)"
cli comment list --issue-id "$IID" >/tmp/linear-e2e-comments.json || fail "comment list"
COUNT=$(python3 -c "
import json
d = json.load(open('/tmp/linear-e2e-comments.json'))
c = d.get('comments') or []
if isinstance(c, dict): c = list(c.values())
print(len(c))
")
echo "comment count: $COUNT"
[[ "$COUNT" -ge 1 ]] || fail "expected at least one comment"

step "delete comment"
cli comment delete --id "$CID" >/tmp/linear-e2e-comment-delete.json || fail "comment delete"

step "verify comment gone (expect 0)"
cli comment list --issue-id "$IID" >/tmp/linear-e2e-comments-after.json || fail "comment list"
COUNT=$(python3 -c "
import json
d = json.load(open('/tmp/linear-e2e-comments-after.json'))
c = d.get('comments') or []
if isinstance(c, dict): c = list(c.values())
print(len(c))
")
[[ "$COUNT" -eq 0 ]] || fail "comment still present after delete"
echo "comment count: 0"

step "delete attachment"
cli attachment delete --id "$AID" >/tmp/linear-e2e-attachment-delete.json || fail "attachment delete"

step "--unassigned filter includes our cleared issue"
cli issue list --team "$TEAM" --unassigned --limit 50 >/tmp/linear-e2e-unassigned.json || fail "issue list --unassigned"
python3 -c "
import json, sys
d = json.load(open('/tmp/linear-e2e-unassigned.json'))
ids = [i['id'] for i in d.get('issues', [])]
if '$IID' not in ids:
  raise SystemExit(f'$IID missing from --unassigned results: {ids[:5]}')
print('$IID present in --unassigned results')
"

if [[ "$KEEP_ISSUE" -eq 0 ]]; then
  step "cancel test issue"
  cli issue save --id "$IID" --state Canceled >/tmp/linear-e2e-cancel.json || fail "cancel"
  CANCELED=$(python3 -c "
import json
i = json.load(open('/tmp/linear-e2e-cancel.json'))['issue']
print(i.get('canceledAt') if isinstance(i, dict) else 'ERR')
")
  [[ "$CANCELED" != "None" && "$CANCELED" != "ERR" ]] || fail "state not canceled"
  echo "canceled at: $CANCELED"
else
  echo
  echo "--kept $IID in its current state"
fi

echo
echo "=== e2e passed. issue: $IID ==="
