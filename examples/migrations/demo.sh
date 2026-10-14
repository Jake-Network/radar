#!/usr/bin/env bash
# Two agents each add an Alembic revision on the same head. Each branch has
# one head; combined there are two, which `alembic upgrade head` refuses.
# Radar reports it statically, and the gate passes once one revision follows
# the other. Usage: demo.sh [PATH_TO_RADAR]. Requires Git.
set -euo pipefail
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-migrations.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar=$(cd "$(dirname "${1:-$RADAR_BIN}")" && pwd)/$(basename "${1:-$RADAR_BIN}")
else
  radar="$workspace/radar"
  (cd "$(dirname "$0")/../.." && go build -o "$radar" ./cmd/radar)
fi
export NO_COLOR=1 RADAR_CONFIG_DIR="$workspace/config" RADAR_STATE_DIR="$workspace/state"
fail() { printf 'migrations demo: %s\n' "$1" >&2; exit 1; }
expect() { # expect FILE TEXT
  grep -qF -- "$2" "$1" || { cat "$1" >&2; fail "expected: $2"; }
}
revision() { # revision FILE ID PARENT
  printf 'revision = "%s"\ndown_revision = "%s"\n\n\ndef upgrade():\n    pass\n' "$2" "$3" > "$1"
}

repo="$workspace/shop"
git init -q -b main "$repo"
cd "$repo"
git config user.name demo
git config user.email demo@radar.invalid
git config core.hooksPath /dev/null
mkdir -p db/versions
printf 'print("env")\n' > db/env.py
printf 'revision = "r0"\ndown_revision = None\n' > db/versions/r0_init.py
git add . && git commit -qm baseline
git worktree add -q -b agent-orders ../orders
git worktree add -q -b agent-refunds ../refunds
revision ../orders/db/versions/a1_orders.py a1 r0
git -C ../orders add . && git -C ../orders commit -qm 'Add orders table'
revision ../refunds/db/versions/b1_refunds.py b1 r0
git -C ../refunds add . && git -C ../refunds commit -qm 'Add refunds table'

for branch in agent-orders agent-refunds; do
  "$radar" gate "$branch" > "$workspace/alone.txt" || fail "$branch should pass alone"
  expect "$workspace/alone.txt" "✓ Migrations"
done

if "$radar" gate > "$workspace/combined.txt"; then
  fail "the combined branches should fail"
fi
expect "$workspace/combined.txt" "Radar gate: FAIL — migration order conflict on the combined tree"
expect "$workspace/combined.txt" "migration_multiple_heads"
expect "$workspace/combined.txt" 'set down_revision = "a1"'
"$radar" gate --json > "$workspace/combined.json" || true
python3 - "$workspace/combined.json" <<'PY' || fail "JSON should require and fail migration_order"
import json, sys
r = json.load(open(sys.argv[1]))
assert "migration_order" in r["gate"]["policy"]["require"]
assert any(c["id"] == "migration_order" and c["status"] == "failed" for c in r["checks"])
PY

# Repair: the refunds revision now follows the orders revision.
revision ../refunds/db/versions/b1_refunds.py b1 a1
git -C ../refunds add . && git -C ../refunds commit -qm 'Follow the orders revision'
"$radar" gate > "$workspace/repaired.txt" || { cat "$workspace/repaired.txt" >&2; fail "the repaired combination should pass"; }
expect "$workspace/repaired.txt" "✓ Migrations  migration history stays in order on the combined tree"
echo "migrations demo: ok"
