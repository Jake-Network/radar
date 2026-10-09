#!/usr/bin/env bash
# Two individually passing multilingual branches, one broken integration.
# Requires Git and Python 3; a prebuilt Radar can be supplied via RADAR_BIN.
set -euo pipefail
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-integration-demo.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
repo="$workspace/checkout"
mkdir "$repo"
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar_bin=$(realpath "${1:-$RADAR_BIN}")
else
  source_root=$(cd "$(dirname "$0")/../.." && pwd)
  radar_bin="$workspace/radar"
  (cd "$source_root" && go build -o "$radar_bin" ./cmd/radar)
fi
git -C "$repo" init -q
git -C "$repo" config user.name 'Radar demo'
git -C "$repo" config user.email demo@radar.invalid
git -C "$repo" config core.hooksPath /dev/null
cat > "$repo/.gitignore" <<'EOF'
.radar/
__pycache__/
EOF
cat > "$repo/backend.py" <<'EOF'
def product_response():
    return {"unit_price": 1}
EOF
cat > "$repo/frontend.ts" <<'EOF'
// Checkout contract: total must stay within the approved two-unit budget.
export const QUANTITY = 1;
EOF
cat > "$repo/test_checkout.py" <<'EOF'
import re
import unittest
from pathlib import Path
from backend import product_response

class CheckoutIntegration(unittest.TestCase):
    def test_combined_budget(self):
        # Read the client-owned checkout configuration rather than duplicate it.
        quantity = int(re.search(r"QUANTITY = (\d+)", Path("frontend.ts").read_text())[1])
        self.assertLessEqual(product_response()["unit_price"] * quantity, 2)
EOF
git -C "$repo" add .
git -C "$repo" commit -qm baseline
git -C "$repo" branch baseline
git -C "$repo" checkout -qb agent-backend
cat > "$repo/backend.py" <<'EOF'
def product_response():
    return {"unit_price": 2}
EOF
git -C "$repo" commit -qam 'Backend agent updates price'
git -C "$repo" checkout -qb agent-frontend baseline
sed -i 's/QUANTITY = 1/QUANTITY = 2/' "$repo/frontend.ts"
git -C "$repo" commit -qam 'Frontend agent updates quantity'
git -C "$repo" checkout -qb repaired agent-frontend
sed -i 's/QUANTITY = 2/QUANTITY = 1/' "$repo/frontend.ts"
git -C "$repo" commit -qam 'Reconcile checkout quantity with backend price'
git -C "$repo" checkout -q baseline
"$radar_bin" init --root "$repo"
for branch in agent-backend agent-frontend; do
  "$radar_bin" merge-check --root "$repo" --base baseline --branches "$branch" --verify --allow-execution --json -- python3 -m unittest test_checkout > "$workspace/$branch.json"
done
set +e
"$radar_bin" merge-check --root "$repo" --base baseline --branches agent-backend,agent-frontend --verify --allow-execution --json -- python3 -m unittest test_checkout > "$workspace/broken.json"
broken_exit=$?
set -e
[[ "$broken_exit" == 1 ]] || { echo "Expected integration failure, received $broken_exit" >&2; exit 1; }
"$radar_bin" merge-check --root "$repo" --base baseline --branches agent-backend,repaired --verify --allow-execution --json -- python3 -m unittest test_checkout > "$workspace/repaired.json"
python3 - "$workspace" <<'PY'
import json, sys
from pathlib import Path
root = Path(sys.argv[1])
def report(name):
    return json.loads((root / (name + '.json')).read_text())
for branch in ['agent-backend', 'agent-frontend']:
    assert report(branch)['execution']['observation']['status'] == 'passed'
broken = report('broken')
assert not broken['conflicts']
assert broken['execution']['observation']['tests_failed'] == 1
assert report('repaired')['execution']['observation']['status'] == 'passed'
assert broken['candidate_tree'] != report('repaired')['candidate_tree']
print('PASS: two individually passing branches merged textually, failed together, and passed after repair.')
for finding in broken['findings']:
    if finding['code'].startswith('integration_execution'):
        print(finding['explanation'])
        print('Repair:', finding['remediation'])
        for location in finding.get('locations', []):
            print('Evidence:', location['path'], 'line', location['line'])
print('Remaining coverage is explicitly incomplete: no declared contract manifest or runtime dependency proof.')
PY
[[ "$(git -C "$repo" rev-parse HEAD)" == "$(git -C "$repo" rev-parse baseline)" ]]
[[ -z "$(git -C "$repo" status --porcelain)" ]]
