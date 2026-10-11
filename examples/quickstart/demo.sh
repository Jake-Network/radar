#!/usr/bin/env bash
# The README's first-run scenario, checked end to end: two agent branches
# that pass alone and fail together, a reproduction outside Radar, a repair,
# and a documentation-only change, which --run cannot verify with tests.
# Usage: demo.sh [PATH_TO_RADAR]. Requires Git and Python 3.
set -euo pipefail
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-quickstart.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar=$(cd "$(dirname "${1:-$RADAR_BIN}")" && pwd)/$(basename "${1:-$RADAR_BIN}")
else
  radar="$workspace/radar"
  (cd "$(dirname "$0")/../.." && go build -o "$radar" ./cmd/radar)
fi
export NO_COLOR=1 RADAR_CONFIG_DIR="$workspace/config" RADAR_STATE_DIR="$workspace/state"
fail() { printf 'quickstart demo: %s\n' "$1" >&2; exit 1; }
expect() { # expect FILE TEXT
  grep -qF -- "$2" "$1" || { cat "$1" >&2; fail "expected: $2"; }
}

repo="$workspace/shop"
git init -q -b main "$repo"
cd "$repo"
git config user.name demo
git config user.email demo@radar.invalid
git config core.hooksPath /dev/null
printf 'def price():\n    return 1\n' > backend.py
printf 'export const QUANTITY = 1;\n' > frontend.ts
cat > test_checkout.py <<'PY'
import re, unittest
from pathlib import Path
from backend import price

class Checkout(unittest.TestCase):
    def test_budget(self):
        quantity = int(re.search(r"QUANTITY = (\d+)", Path("frontend.ts").read_text())[1])
        self.assertLessEqual(price() * quantity, 2)
PY
printf '# shop\n' > README.md
git add . && git commit -qm baseline
git worktree add -q -b agent-backend ../backend
git worktree add -q -b agent-frontend ../frontend
printf 'def price():\n    return 2\n' > ../backend/backend.py
git -C ../backend commit -qam 'Raise price'
printf 'export const QUANTITY = 2;\n' > ../frontend/frontend.ts
git -C ../frontend commit -qam 'Raise quantity'

for dir in . ../backend ../frontend; do
  (cd "$dir" && python3 -m unittest -q >/dev/null 2>&1) || fail "$dir should pass alone"
done
echo 'PASS: base and each agent branch pass their own tests.'

"$radar" doctor > "$workspace/doctor.txt"
expect "$workspace/doctor.txt" 'agent-backend, agent-frontend'
expect "$workspace/doctor.txt" 'Next: radar gate'

"$radar" gate > "$workspace/static.txt" || fail 'static gate should exit 0'
expect "$workspace/static.txt" 'PASS (static)'
expect "$workspace/static.txt" 'Next: radar gate --run'

set +e
"$radar" gate --run > "$workspace/run.txt"
code=$?
set -e
[[ $code == 1 ]] || { cat "$workspace/run.txt" >&2; fail "combined run should exit 1, got $code"; }
expect "$workspace/run.txt" 'Radar gate: FAIL'
expect "$workspace/run.txt" 'test_checkout.Checkout.test_budget'
echo 'PASS: Radar reports the combined failure.'

repro=$(sed -n 's/^    reproduce: //p' "$workspace/run.txt")
[[ -n $repro ]] || fail 'no reproduce line'
before=$(git -C "$repo" status --porcelain; git -C "$repo" rev-parse HEAD)
worktrees=$(git -C "$repo" worktree list | wc -l)
set +e
(cd "$repo" && TMPDIR="$workspace" bash -c "$repro" >/dev/null 2>&1)
code=$?
set -e
[[ $code != 0 ]] || fail 'the reproduce command should fail like the gate'
[[ $(git -C "$repo" worktree list | wc -l) -gt $worktrees ]] || fail 'reproduction worktree missing'
[[ $before == "$(git -C "$repo" status --porcelain; git -C "$repo" rev-parse HEAD)" ]] || fail 'reproduction touched the main checkout'
echo 'PASS: the reproduce line fails the same test outside Radar, in its own worktree.'

printf 'def price():\n    return 1\n' > ../backend/backend.py
git -C ../backend commit -qam 'Keep price within budget'
"$radar" gate --run > "$workspace/repaired.txt" || true
expect "$workspace/repaired.txt" '✓ Tests'
echo 'PASS: after the repair the combined tests pass.'

git worktree add -q -b agent-docs ../docs main
printf '# shop\n\nHow to buy.\n' > ../docs/README.md
git -C ../docs commit -qam 'Document buying'
set +e
"$radar" gate --run agent-docs > "$workspace/docs.txt"
code=$?
set -e
[[ $code == 1 ]] || { cat "$workspace/docs.txt" >&2; fail "docs-only run has no test evidence and should exit 1, got $code"; }
expect "$workspace/docs.txt" 'NOT VERIFIED — only documentation changed, so no test was selected and none ran'
expect "$workspace/docs.txt" 'the static check is what Radar verifies: radar gate'
echo 'PASS: a documentation-only change is not passed by --run, and the report says why.'
