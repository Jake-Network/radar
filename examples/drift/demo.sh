#!/usr/bin/env bash
# Saved changes in two worktrees: no agent commits are necessary.
set -euo pipefail
RADAR="${1:-radar}"
if [[ "$RADAR" == */* ]]; then RADAR="$(cd "$(dirname "$RADAR")" && pwd)/$(basename "$RADAR")"; fi
DEMO_DIR="$(mktemp -d)"
trap 'rm -rf "$DEMO_DIR"' EXIT
export RADAR_CONFIG_DIR="$DEMO_DIR/config" RADAR_STATE_DIR="$DEMO_DIR/state"
mkdir "$DEMO_DIR/repo"
cd "$DEMO_DIR/repo"
git init -q -b main
git config user.name Demo
git config user.email demo@example.invalid
printf 'def charge():\n    return 1\n' > backend.py
printf 'VALUE = 1\n' > client.py
git add .
git commit -qm baseline
git worktree add -qb agent-api "$DEMO_DIR/api"
git worktree add -qb agent-client "$DEMO_DIR/client"
printf 'def bill():\n    return 1\n' > "$DEMO_DIR/api/backend.py"
printf 'from backend import charge\nVALUE = charge()\n' > "$DEMO_DIR/client/client.py"
printf 'Both agents have only saved their files. Radar inspects their private combined snapshots.\n'
if "$RADAR" gate --overlap --base main --json > "$DEMO_DIR/overlap.json"; then echo "expected drift failure" >&2; exit 1; else test "$?" -eq 1; fi
python3 - "$DEMO_DIR/overlap.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
assert r['gate']['verdict'] == 'fail', r
assert any(f['code'] == 'symbol_dangling_reference' for f in r['drift']), r
for f in r['drift']:
    print(f['explanation'])
    for p in f['locations']:
        print(f"  {p['path']}:{p.get('line', 0)}")
PY
python3 - "$DEMO_DIR/client" <<'PY' | "$RADAR" hook stop --agent claude --base main > "$DEMO_DIR/hook.json"
import json, sys
print(json.dumps({'cwd':sys.argv[1], 'hook_event_name':'Stop'}))
PY
python3 - "$DEMO_DIR/hook.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
assert r['decision'] == 'block', r
assert 'cross-worktree drift' in r['reason'], r
print(r['reason'])
PY
printf 'from backend import bill\nVALUE = bill()\n' > "$DEMO_DIR/client/client.py"
"$RADAR" gate --overlap --base main --json > "$DEMO_DIR/repaired.json"
python3 - "$DEMO_DIR/repaired.json" <<'PY'
import json, sys
r = json.load(open(sys.argv[1]))
assert not r.get('drift'), r
print('No supported drift after repair; runtime compatibility remains unverified.')
PY
