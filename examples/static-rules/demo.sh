#!/usr/bin/env bash
# Reproduce supported static conflicts in real temporary Git repositories.
# Application source is never executed. Usage: demo.sh [PATH_TO_RADAR].
set -euo pipefail
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-static-rules.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar=$(cd "$(dirname "${1:-$RADAR_BIN}")" && pwd)/$(basename "${1:-$RADAR_BIN}")
else
  radar="$workspace/radar"
  (cd "$(dirname "$0")/../.." && go build -o "$radar" ./cmd/radar)
fi
export NO_COLOR=1 RADAR_CONFIG_DIR="$workspace/config" RADAR_STATE_DIR="$workspace/state"
fail() { printf 'static rules demo: %s\n' "$1" >&2; exit 1; }
init_repo() {
  git init -q -b main "$1"
  cd "$1"
  git config user.name demo
  git config user.email demo@radar.invalid
  git config core.hooksPath /dev/null
}
assert_gate() { # expected verdict, required check ID, optional finding code, branches...
  local verdict=$1 check=$2 finding=$3 code=0
  shift 3
  "$radar" gate "$@" --json > "$workspace/report.json" || code=$?
  if [[ "$verdict" == pass ]]; then
    [[ "$code" == 0 ]] || { cat "$workspace/report.json" >&2; fail "expected static PASS (exit 0)"; }
  else
    [[ "$code" == 1 ]] || { cat "$workspace/report.json" >&2; fail "expected static FAIL (exit 1)"; }
  fi
  python3 - "$workspace/report.json" "$verdict" "$check" "$finding" <<'PY'
import json, sys
report = json.load(open(sys.argv[1], encoding="utf-8"))
verdict, check, finding = sys.argv[2:]
assert report["gate"]["verdict"] == verdict, report
assert check in report["gate"]["policy"]["require"], report
assert any(c["id"] == check and c["status"] == ("passed" if verdict == "pass" else "failed") for c in report["checks"]), report
assert not report.get("executions"), "Repository execution must remain absent"
if finding:
    assert any(f["code"] == finding for f in report["findings"]), report
PY
}

# A removes a declaration; B adds a named import of the old declaration.
init_repo "$workspace/symbols"
printf 'def foo():\n    return 1\n' > lib.py
git add . && git commit -qm baseline
git worktree add -q -b agent-rename ../rename
git worktree add -q -b agent-consumer ../consumer
printf 'def bar():\n    return 1\n' > ../rename/lib.py
git -C ../rename commit -qam 'Rename the unused producer'
printf 'from lib import foo\n\ndef value():\n    return foo()\n' > ../consumer/consumer.py
git -C ../consumer add . && git -C ../consumer commit -qm 'Add an old-name consumer'
assert_gate pass no_symbol_conflicts '' agent-rename
assert_gate pass no_symbol_conflicts '' agent-consumer
assert_gate fail no_symbol_conflicts symbol_dangling_reference agent-rename agent-consumer
printf '\nSeparate symbol branches: PASS (static). Together:\n'
"$radar" gate agent-rename agent-consumer || [[ "$?" == 1 ]]
# Fix B to refer to the new name. Run only on the combination; it now depends on A.
printf 'from lib import bar\n\ndef value():\n    return bar()\n' > ../consumer/consumer.py
git -C ../consumer commit -qam 'Use the renamed producer'
assert_gate pass no_symbol_conflicts '' agent-rename agent-consumer

# Explicit user rule: two separate files claim the same error code.
init_repo "$workspace/unique"
mkdir -p .radar config
cat > .radar/unique.json <<'JSON'
{"version":1,"rules":[{"name":"error_code","files":["config/*.txt"],"pattern":"ERROR_CODE = \"(E[0-9]+)\""}]}
JSON
printf 'ERROR_CODE = "E1000"\n' > config/base.txt
git add . && git commit -qm baseline
git worktree add -q -b agent-orders ../orders
git worktree add -q -b agent-refunds ../refunds
printf 'ERROR_CODE = "E1001"\n' > ../orders/config/orders.txt
git -C ../orders add . && git -C ../orders commit -qm 'Add orders error code'
printf 'ERROR_CODE = "E1001"\n' > ../refunds/config/refunds.txt
git -C ../refunds add . && git -C ../refunds commit -qm 'Add refunds error code'
assert_gate pass unique_keys '' agent-orders
assert_gate pass unique_keys '' agent-refunds
assert_gate fail unique_keys duplicate_unique_key agent-orders agent-refunds
printf '\nSeparate unique-key branches: PASS (static). Together:\n'
"$radar" gate agent-orders agent-refunds || [[ "$?" == 1 ]]
printf 'ERROR_CODE = "E1002"\n' > ../refunds/config/refunds.txt
git -C ../refunds commit -qam 'Choose a distinct refunds error code'
assert_gate pass unique_keys '' agent-orders agent-refunds
printf '\nstatic rules demo: ok (no repository code executed)\n'
