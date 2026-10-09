#!/usr/bin/env bash
# Offline generated fixture: actual HTTP + Node fetch, static FastAPI/TS adapters.
set -euo pipefail
command -v git >/dev/null
command -v python3 >/dev/null
command -v node >/dev/null
[[ -x /usr/bin/time ]] || { echo 'Demo metrics require /usr/bin/time (Linux).' >&2; exit 2; }
source_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-intelligent-demo.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
repo="$workspace/checkout"
cp -R "$source_dir/fixture" "$repo"
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar_bin=$(realpath "${1:-$RADAR_BIN}")
else
  radar_bin="$workspace/radar"
  (cd "$source_dir/../.." && go build -o "$radar_bin" ./cmd/radar)
fi
git -C "$repo" init -q
git -C "$repo" config user.name 'Radar generated demo'
git -C "$repo" config user.email demo@radar.invalid
git -C "$repo" config core.hooksPath /dev/null
git -C "$repo" add .
git -C "$repo" commit -qm 'Generated checkout baseline'
git -C "$repo" branch baseline
git -C "$repo" checkout -qb agent-backend
python3 - "$repo/backend/service.py" <<'PY'
import pathlib, sys
p=pathlib.Path(sys.argv[1]); p.write_text(p.read_text().replace('UNIT_PRICE = 1', 'UNIT_PRICE = 2'))
PY
git -C "$repo" commit -qam 'Backend agent updates catalog price'
git -C "$repo" checkout -qb agent-frontend baseline
python3 - "$repo/frontend/cart.mjs" <<'PY'
import pathlib, sys
p=pathlib.Path(sys.argv[1]); p.write_text(p.read_text().replace('quantity = 1', 'quantity = 2'))
PY
git -C "$repo" commit -qam 'Frontend agent updates cart quantity'
git -C "$repo" checkout -qb repaired agent-frontend
python3 - "$repo/frontend/cart.mjs" <<'PY'
import pathlib, sys
p=pathlib.Path(sys.argv[1]); p.write_text(p.read_text().replace('quantity = 2', 'quantity = 1'))
PY
git -C "$repo" commit -qam 'Reconcile quantity with catalog price'
git -C "$repo" checkout -q baseline

run_expected() {
  local expected=$1 report=$2
  shift 2
  local status=0
  /usr/bin/time -q -f '%e %M' -o "$report.time" "$@" > "$report" 2> "$report.stderr" || status=$?
  [[ "$status" == "$expected" ]] || {
    echo "Expected exit $expected; received $status: $*" >&2
    cat "$report.stderr" "$report" >&2
    exit 1
  }
}
cat > "$workspace/suite.sh" <<'SUITE'
#!/usr/bin/env bash
set -euo pipefail
(cd "$1/backend" && python3 -m unittest discover -s tests -p 'test_*.py')
(cd "$1/frontend" && node --test --test-reporter=tap cart.test.mjs)
(cd "$1" && python3 -m unittest discover -s integration -p 'test_*.py')
SUITE
for branch in agent-backend agent-frontend; do
  # Establish independently passing full fixture suites, including the HTTP
  # integration test, separately from Radar's narrower recommendations.
  individual="$workspace/individual-$branch"
  cp -R "$repo" "$individual"
  git -C "$individual" checkout -q "$branch"
  run_expected 0 "$workspace/individual-$branch.log" bash "$workspace/suite.sh" "$individual"
  run_expected 0 "$workspace/$branch.json" "$radar_bin" merge-check --root "$repo" \
    --base baseline --branches "$branch" --suggest-tests --verify --allow-execution \
    --suite recommended --policy gate.json --json
done
run_expected 0 "$workspace/suggestions.json" "$radar_bin" merge-check --root "$repo" \
  --base baseline --branches agent-backend,agent-frontend --suggest-tests --json

cat > "$workspace/manual.sh" <<'MANUAL'
#!/usr/bin/env bash
set -euo pipefail
git -C "$1" merge --no-ff --no-edit agent-backend >/dev/null
git -C "$1" merge --no-ff --no-edit agent-frontend >/dev/null
bash "$2" "$1"
MANUAL
for run in 1 2 3; do
  manual="$workspace/manual-$run"
  cp -R "$repo" "$manual"
  run_expected 1 "$workspace/manual-$run.log" bash "$workspace/manual.sh" "$manual" "$workspace/suite.sh"
  run_expected 1 "$workspace/broken-$run.json" "$radar_bin" merge-check --root "$repo" \
    --base baseline --branches agent-backend,agent-frontend --suggest-tests --verify \
    --allow-execution --suite recommended --policy gate.json --json
done
run_expected 0 "$workspace/repaired.json" "$radar_bin" merge-check --root "$repo" \
  --base baseline --branches agent-backend,repaired --suggest-tests --verify \
  --allow-execution --suite recommended --policy gate.json --json

python3 - "$workspace" <<'PY'
import json, pathlib, sys
root=pathlib.Path(sys.argv[1])
def report(name): return json.loads((root/(name+'.json')).read_text())
for branch in ['agent-backend', 'agent-frontend']:
    assert report(branch)['gate']['verdict']=='pass', report(branch)
broken=report('broken-1'); repaired=report('repaired')
assert not broken['conflicts'], broken
assert broken['gate']['verdict']=='fail', broken
assert any(e['status']=='failed' for e in broken['executions']), broken
assert repaired['gate']['verdict']=='pass', repaired
assert broken['candidate_tree']!=repaired['candidate_tree']
assert any('integration/test_checkout_integration.py' in c['test_files'] for c in report('suggestions')['verification_proposal']['commands'])
print('PASS: two independently passing branches, conflict-free merge, actual HTTP/client integration failure, repaired recommended suite passes.')
for c in report('suggestions')['verification_proposal']['commands']:
    print('Recommended:', c['cwd'], c['command'])
    print('Selection reasons:', ', '.join(r['code'] for r in c['evidence_reasons']))
print('Measured wall seconds and max RSS KiB (three runs; generated fixture, Linux temporary filesystem):')
for label,stem in [('Manual Git merge + all three test commands','manual'), ('Radar analysis + recommended tests','broken')]:
    samples=[]
    for i in [1,2,3]:
        suffix='log' if stem=='manual' else 'json'
        sample=(root/f'{stem}-{i}.{suffix}.time').read_text().strip()
        if float(sample.split()[0]) < 0:
            sample='unavailable: wall clock moved backwards during measurement'
        samples.append(sample)
    print(label+': '+ '; '.join(samples))
print('Scope: generated fixture; FastAPI/Pydantic adapters and TS types are statically analyzed, not runtime imported/typechecked. Runtime uses stdlib HTTP and actual Node fetch. No package downloads. No adoption or scalability claim.')
print('A passing selected policy leaves unrelated compiler/runtime analysis coverage incomplete.')
PY
[[ "$(git -C "$repo" rev-parse HEAD)" == "$(git -C "$repo" rev-parse baseline)" ]]
[[ -z "$(git -C "$repo" status --porcelain)" ]]
if [[ -n "${DEMO_ARTIFACT_DIR:-}" ]]; then
  mkdir -p "$DEMO_ARTIFACT_DIR"
  cp "$workspace"/*.json "$workspace"/*.time "$DEMO_ARTIFACT_DIR/"
fi
