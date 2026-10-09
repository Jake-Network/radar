#!/usr/bin/env bash
# Gate safety and bounded test selection on the evaluation fixture.
# Requires Git, Python 3, Node and Go; a prebuilt Radar can be supplied as $1
# or RADAR_BIN. Only the private merge-check candidate executes test code.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-selection-demo.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar=$(realpath "${1:-$RADAR_BIN}")
else
  radar="$workspace/radar"
  (cd "$here/../.." && go build -o "$radar" ./cmd/radar)
fi
repo="$workspace/repo"
cp -R "$here/fixture" "$repo"
git -C "$repo" init -q
git -C "$repo" config user.name 'Radar demo'
git -C "$repo" config user.email demo@radar.invalid
git -C "$repo" config core.hooksPath /dev/null
printf '__pycache__/\n' > "$repo/.gitignore"
printf '{"version":1,"require":["textual_merge","no_breaking_contracts","integration_execution"]}\n' > "$workspace/policy.json"
git -C "$repo" add -A && git -C "$repo" commit -qm baseline
base=$(git -C "$repo" rev-parse HEAD)

expect() { # expect EXIT_CODE VERDICT -- radar args...
  local want=$1 verdict=$2; shift 3
  set +e; out=$("$radar" --root "$repo" --json "$@"); code=$?; set -e
  got=$(printf '%s' "$out" | python3 -c 'import json,sys; print(json.load(sys.stdin)["gate"]["verdict"])')
  printf '  exit %s, gate %s\n' "$code" "$got"
  [[ $code == "$want" && $got == "$verdict" ]] || { echo "unexpected result (want exit $want, $verdict)"; exit 1; }
}

echo "1. Producer renames a consumed field AND deletes the contract manifest:"
git -C "$repo" checkout -qb drop-manifest "$base"
sed -i 's/"currency": currency/"currency_code": currency/' "$repo/services/api/app/orders.py"
sed -i 's/"currency"/"currency_code"/g' "$repo/contracts/order-summary.schema.json"
git -C "$repo" rm -q .radar/contracts.json
git -C "$repo" commit -qam 'rename currency, drop manifest'
expect 1 fail -- check --base "$base" --head drop-manifest --policy "$workspace/policy.json"
echo "   The base manifest still defines the obligation; deleting it cannot pass the gate."

echo "2. Same rename with an explicit, reviewable retirement record:"
git -C "$repo" checkout -qb retire "$base"
sed -i 's/"currency": currency/"currency_code": currency/' "$repo/services/api/app/orders.py"
sed -i 's/"currency"/"currency_code"/g' "$repo/contracts/order-summary.schema.json"
printf '{"version":1,"bindings":[],"retired":[{"id":"order-summary","reason":"storefront migrates to currency_code in the same release"}]}\n' > "$repo/.radar/contracts.json"
git -C "$repo" commit -qam 'rename currency, retire binding'
printf '{"version":1,"require":["no_breaking_contracts"]}\n' > "$workspace/contracts-policy.json"
expect 0 pass -- check --base "$base" --head retire --policy "$workspace/contracts-policy.json"
echo "   Contract gate passes and reports contract_obligation_retired; tests still decide behavior:"
expect 1 fail -- merge-check --base "$base" --branches retire --policy "$workspace/policy.json" --verify --allow-execution --suite targeted
echo "   The targeted suite ran the storefront contract test against the combined candidate and observed the break."

echo "3. Go clock regression, targeted suite with a one-command budget:"
git -C "$repo" checkout -qb clock "$base"
sed -i 's|m.now.Add(d)|m.now.Add(d / 2)|' "$repo/gosvc/clock/clock.go"
git -C "$repo" commit -qam 'clock regression'
"$radar" --root "$repo" check --base "$base" --head clock --suite targeted --max-commands 1 | sed -n '/^Selection/,/^  [A-Z]/p'
expect 1 fail -- merge-check --base "$base" --branches clock --policy "$workspace/policy.json" --verify --allow-execution --suite targeted
echo "   Two Go packages were grouped into one invocation; the failure is observed, not inferred."

echo "4. Documentation-only change selects no tests and blocks nothing in selection:"
git -C "$repo" checkout -qb docs "$base"
echo "More docs." >> "$repo/README.md"
git -C "$repo" commit -qam docs
"$radar" --root "$repo" check --base "$base" --head docs --suite targeted | grep -E '^Selection|No changed code'
