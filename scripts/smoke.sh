#!/usr/bin/env bash
# Portable release smoke test (Linux and macOS; no GNU-only tools):
# two agent worktrees that pass alone and fail together.
# Usage: smoke.sh PATH_TO_RADAR. Requires Git and Python 3.
set -euo pipefail
radar=$(cd "$(dirname "$1")" && pwd)/$(basename "$1")
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-smoke.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
repo="$workspace/repo"
git init -q "$repo"
git -C "$repo" symbolic-ref HEAD refs/heads/main
git -C "$repo" config user.name 'Radar smoke'
git -C "$repo" config user.email smoke@radar.invalid
git -C "$repo" config core.hooksPath /dev/null
printf 'def price():\n    return 1\n' > "$repo/backend.py"
printf 'export const QUANTITY = 1;\n' > "$repo/frontend.ts"
cat > "$repo/test_checkout.py" <<'EOF'
import re, unittest
from pathlib import Path
from backend import price

class Checkout(unittest.TestCase):
    def test_budget(self):
        quantity = int(re.search(r"QUANTITY = (\d+)", Path("frontend.ts").read_text())[1])
        self.assertLessEqual(price() * quantity, 2)
EOF
git -C "$repo" add .
git -C "$repo" commit -qm baseline
git -C "$repo" worktree add -q -b agent-backend "$workspace/backend" main
printf 'def price():\n    return 2\n' > "$workspace/backend/backend.py"
git -C "$workspace/backend" commit -qam 'Backend agent raises price'
git -C "$repo" worktree add -q -b agent-frontend "$workspace/frontend" main
printf 'export const QUANTITY = 2;\n' > "$workspace/frontend/frontend.ts"
git -C "$workspace/frontend" commit -qam 'Frontend agent raises quantity'

"$radar" version
"$radar" help >/dev/null
"$radar" gate --root "$repo" >/dev/null
for branch in agent-backend agent-frontend; do
    # Runtime reads of frontend.ts are invisible to static import selection.
    "$radar" gate --root "$repo" --run --suite full "$branch" >/dev/null
done
set +e
"$radar" gate --root "$repo" --run > "$workspace/combined.txt"
code=$?
set -e
if [[ "$code" != 1 ]] || ! grep -q 'Radar gate: FAIL' "$workspace/combined.txt"; then
    cat "$workspace/combined.txt" >&2
    printf 'Expected the combined branches to fail (exit 1), got %s.\n' "$code" >&2
    exit 1
fi
printf 'Smoke test passed: each branch passes alone, the combination fails.\n'
