#!/usr/bin/env bash
# Reproduce committed cross-repository link checks without touching user repos.
set -euo pipefail
radar_bin=${1:-/tmp/radar}
radar_bin=$(realpath "$radar_bin")
demo_root=$(mktemp -d "${TMPDIR:-/tmp}/radar-workspace-links.XXXXXX")
trap 'rm -rf "$demo_root"' EXIT
export RADAR_CONFIG_DIR="$demo_root/config"
export RADAR_STATE_DIR="$demo_root/state"
orders="$demo_root/orders"
payments="$demo_root/payments"
for repo in "$orders" "$payments"; do
  mkdir -p "$repo"
  git -C "$repo" init -q -b main
  git -C "$repo" config user.name 'Radar demo'
  git -C "$repo" config user.email 'radar@example.invalid'
done
cat > "$orders/openapi.json" <<'JSON'
{"type":"object","properties":{"total":{"type":"number"},"status":{"type":"string"}}}
JSON
printf 'TOTAL = 1\n' > "$payments/client.py"
for repo in "$orders" "$payments"; do
  git -C "$repo" add .
  git -C "$repo" commit -qm baseline
done
printf '\n$ radar workspace add <payments> --name shop\n'
"$radar_bin" --root "$orders" workspace add "$payments" --name shop
printf '\n$ radar workspace connect orders:openapi.json# payments --fields total,status --direction response --id order-response --source client.py\n'
"$radar_bin" --root "$orders" workspace connect orders:openapi.json# payments --fields total,status --direction response --id order-response --source client.py
printf '\n$ git add and commit declarations in each repository\n'
git -C "$orders" add .radar/workspace.json
git -C "$orders" commit -qm 'Declare order-response'
git -C "$payments" add .radar/consumes.json
git -C "$payments" commit -qm 'Declare consumed fields'
git -C "$orders" worktree add -q -b agent/api "$demo_root/agent-api" main
printf 'REVISION = 1\n' > "$demo_root/agent-api/revision.py"
git -C "$demo_root/agent-api" add revision.py
git -C "$demo_root/agent-api" commit -qm 'Prepare API revision'
printf '\n$ radar gate orders:agent/api  # static PASS\n'
"$radar_bin" --root "$orders" gate orders:agent/api
printf '\n$ commit producer field removal on agent/api\n'
cat > "$demo_root/agent-api/openapi.json" <<'JSON'
{"type":"object","properties":{"status":{"type":"string"}}}
JSON
git -C "$demo_root/agent-api" add openapi.json
git -C "$demo_root/agent-api" commit -qm 'Remove total'
printf '\n$ radar gate --again  # expected FAIL, exit 1\n'
set +e
"$radar_bin" --root "$payments" gate --again
status=$?
set -e
if [ "$status" -ne 1 ]; then echo "expected exit 1, got $status" >&2; exit 1; fi
printf '\n$ restore total and commit on agent/api\n'
git -C "$demo_root/agent-api" show main:openapi.json > "$demo_root/agent-api/openapi.json"
git -C "$demo_root/agent-api" add openapi.json
git -C "$demo_root/agent-api" commit -qm 'Retain consumed total'
printf '\n$ radar gate --again  # repaired static PASS\n'
"$radar_bin" --root "$orders" gate --again
printf '\n$ radar gate --replay last  # reproduce pinned team and commits\n'
"$radar_bin" --root "$payments" gate --replay last
