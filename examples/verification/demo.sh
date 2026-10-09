#!/usr/bin/env bash
set -euo pipefail
radar_binary=${1:?usage: demo.sh /absolute/path/to/radar}
[[ "$radar_binary" == /* ]] || { echo 'Use an absolute binary path' >&2; exit 2; }
demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/radar-verification.XXXXXX")
cd "$demo_dir"
git init -q
git config user.email radar-demo@example.invalid
git config user.name 'Radar verification fixture'
printf '.radar/\n__pycache__/\n' > .gitignore
printf 'def summary():\n    return {"total": 1}\n' > app.py
cat > test_app.py <<'PY_TEST'
import unittest
from app import summary
class SummaryTest(unittest.TestCase):
    def test_summary(self):
        self.assertEqual(summary(), {"total": 1, "currency": "USD"})
PY_TEST
printf 'export interface Summary { total: number; }\n' > consumer.ts
printf '{"type":"object","properties":{"total":{"type":"integer"}},"required":["total"]}\n' > schema.json
"$radar_binary" init
cat > .radar/contracts.json <<'JSON_BINDINGS'
{"version":1,"bindings":[{"id":"summary","schema":"schema.json","pointer":"","producer":"app.py","consumer":"consumer.ts","direction":"response","fields":["total"]}]}
JSON_BINDINGS
git add .gitignore app.py test_app.py consumer.ts schema.json
git add -f .radar/contracts.json
git commit -qm 'Baseline declared producer consumer contract'
base_revision=$(git rev-parse HEAD)
"$radar_binary" index --ref "$base_revision" --json > .radar/base-index.json
python3 - "$base_revision" <<'PY_PLAN'
import json, pathlib, sys
snapshot=json.load(open('.radar/base-index.json'))
files={node['provenance']['path']:node['id'] for node in snapshot['nodes'] if node['kind']=='file'}
expected = {"type":"object","properties":{"total":{"type":"integer"},"currency":{"type":"string"}},"required":["total"]}
plan = {"schema_version":"1","feature_id":"summary-currency","intent":"Add summary currency while retaining total","base_revision":sys.argv[1],
"requirements":[{"id":"summary","intent":"Retain total and add currency"}],
"decisions":[{"id":"additive","intent":"Keep existing response dependency","alternatives":["Replace total","Add currency"],"tradeoffs":["Additive schema keeps old consumer declaration valid"]}],
"constraints":[],"contract_deltas":[{"contract":"summary","operation":"modify","description":"Add currency property","schema":"schema.json","pointer":"","expected":expected,"consumers":[files["consumer.ts"]]}],
"tasks":[{"id":"implement","intent":"Implement additive summary response","requirements":["summary"],"acceptance":["total-present","summary-test"],"depends_on":[],"contracts":["summary"],"components":[files["app.py"]],"consequential":True}],
"acceptance":[{"id":"total-present","requirement":"summary","intent":"Keep total schema property","rule":{"kind":"json_property","path":"schema.json","pointer":"/properties","property":"total"}},{"id":"summary-test","requirement":"summary","intent":"Execute summary regression test","rule":{"kind":"test_run","command":["python3","-m","unittest","test_app"]}}],
"graph_deltas":[],"assumptions":[],"evidence":[],"incomplete":[]}
pathlib.Path('.radar/candidate.json').write_text(json.dumps(plan,indent=2)+'\n')
pathlib.Path('.radar/expected-schema.json').write_text(json.dumps(expected)+'\n')
PY_PLAN
# Fixture review is a declared test identity, never a simulated human approval.
"$radar_binary" approve --plan .radar/candidate.json --reviewer fixture-declaration --output .radar/approved.json --json
"$radar_binary" preflight --plan .radar/approved.json --ref "$base_revision" --json
"$radar_binary" tasks --plan .radar/approved.json --json
printf 'def summary():\n    return {"total": 1, "currency": "USD"}\n' > app.py
cp .radar/expected-schema.json schema.json
git add app.py schema.json
git commit -qm 'Implement approved additive summary'
compliant_revision=$(git rev-parse HEAD)
"$radar_binary" test --plan .radar/approved.json --ref "$compliant_revision" --allow-execution --timeout 30s --json -- python3 -m unittest test_app > .radar/pass-evidence.json
pass_evidence=$(python3 -c 'import json; print(json.load(open(".radar/pass-evidence.json"))["id"])')
"$radar_binary" verify --plan .radar/approved.json --ref "$compliant_revision" --evidence "$pass_evidence" --json > .radar/pass-report.json
cat .radar/pass-report.json
python3 - <<'PY_ASSERT_PASS'
import json
report = json.load(open('.radar/pass-report.json'))
assert report['status'] == 'passed', report
assert report['authoritative'] is True, report
assert any(c['id']=='summary-test' and c['status']=='passed' for c in report['checks']), report
PY_ASSERT_PASS
# Editing design invalidates both its review and previously collected evidence.
python3 - <<'PY_STALE_PLAN'
import json
p=json.load(open('.radar/approved.json')); p['intent'] += ' amended'; open('.radar/amended.json','w').write(json.dumps(p)+'\n')
PY_STALE_PLAN
"$radar_binary" verify --plan .radar/amended.json --ref "$compliant_revision" --evidence "$pass_evidence" --json > .radar/stale-plan-report.json || true
python3 - <<'PY_ASSERT_STALE_PLAN'
import json
r=json.load(open('.radar/stale-plan-report.json'))
assert r['authoritative'] is False, r
assert any(c['id']=='summary-test' and c['status']=='unknown' for c in r['checks']), r
PY_ASSERT_STALE_PLAN
# Same command at a different implementation commit cannot reuse passing evidence.
printf 'def summary():\n    return {"total_cents": 100, "currency": "USD"}\n' > app.py
python3 - <<'PY_BREAK'
import json
p='schema.json'; d=json.load(open(p)); d['properties']['total_cents']=d['properties'].pop('total'); d['required']=['total_cents']; open(p,'w').write(json.dumps(d)+'\n')
PY_BREAK
git add app.py schema.json
git commit -qm 'Introduce noncompliant producer contract'
broken_revision=$(git rev-parse HEAD)
if "$radar_binary" verify --plan .radar/approved.json --ref "$broken_revision" --evidence "$pass_evidence" --json > .radar/stale-report.json; then
  echo 'Expected broken contract verification to fail' >&2; exit 1
fi
if "$radar_binary" test --plan .radar/approved.json --ref "$broken_revision" --allow-execution --timeout 30s --json -- python3 -m unittest test_app > .radar/fail-evidence.json; then
  echo 'Expected real unittest failure' >&2; exit 1
fi
fail_evidence=$(python3 -c 'import json; print(json.load(open(".radar/fail-evidence.json"))["id"])')
if "$radar_binary" verify --plan .radar/approved.json --ref "$broken_revision" --evidence "$fail_evidence" --json > .radar/fail-report.json; then
  echo 'Expected noncompliant implementation to fail' >&2; exit 1
fi
python3 - <<'PY_ASSERT_FAIL'
import json
stale=json.load(open('.radar/stale-report.json')); failed=json.load(open('.radar/fail-report.json'))
assert any(c['id']=='summary-test' and c['status']=='unknown' for c in stale['checks']), stale
assert failed['status']=='failed' and failed['authoritative'] is True, failed
assert any(c['id']=='summary-test' and c['status']=='failed' for c in failed['checks']), failed
print('Verified compliant pass, noncompliant failure, and stale-commit evidence rejection.')
PY_ASSERT_FAIL
cat .radar/fail-report.json
printf 'Verification fixture retained at: %s\n' "$demo_dir"
