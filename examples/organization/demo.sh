#!/usr/bin/env bash
set -euo pipefail

# Run explicitly: bash examples/organization/demo.sh /absolute/path/to/radar
# All Git mutations are confined to a fresh temporary fixture copy.
radar_binary=${1:?usage: demo.sh /absolute/path/to/radar}
if [[ "$radar_binary" != /* ]]; then
  echo "Provide an absolute Radar binary path" >&2
  exit 2
fi
fixture_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
demo_dir=$(mktemp -d "${TMPDIR:-/tmp}/radar-demo.XXXXXX")
cp -R "$fixture_dir/." "$demo_dir/"
cd "$demo_dir"
git init -q
git config user.email radar-demo@example.invalid
git config user.name "Radar demo"
printf '\n.radar/state.db*\n.radar/config.json\n.radar/plan.json\n.radar/index.json\n.radar/generated-plan.json\n' >> .gitignore
git add .
git commit -qm 'Baseline multilingual contract fixture'
base_revision=$(git rev-parse HEAD)
"$radar_binary" init
"$radar_binary" index --ref "$base_revision" --json > .radar/index.json
cat .radar/index.json
python3 - <<'PY'
from pathlib import Path
import json
snapshot = json.loads(Path('.radar/index.json').read_text())
for path in Path('plans').glob('*.json'):
    plan = json.loads(path.read_text())
    plan['base_revision'] = snapshot['revision']
    path.write_text(json.dumps(plan, indent=2) + '\n')
PY
"$radar_binary" plan --ref "$base_revision" --output .radar/generated-plan.json "Implement asynchronous organization-scoped exports" --json
"$radar_binary" graph --name ExportSummary --json
"$radar_binary" preflight --plan plans/exports.json --ref "$base_revision" --json
if "$radar_binary" preflight --plan plans/exports-inconsistent.json --ref "$base_revision" --json; then
  echo "Expected inconsistent plan to fail" >&2
  exit 1
fi
"$radar_binary" tasks --plan plans/exports.json --json
"$radar_binary" verify --plan plans/exports.json --ref "$base_revision" --json

git checkout -qb producer
python3 - <<'PY'
from pathlib import Path
import json
p = Path('contracts/openapi.json')
doc = json.loads(p.read_text())
schema = doc['components']['schemas']['ExportSummary']
schema['properties']['total_cents'] = schema['properties'].pop('total')
schema['required'] = ['dataset_id', 'total_cents']
p.write_text(json.dumps(doc, indent=2) + '\n')
p = Path('backend/models.py')
p.write_text(p.read_text().replace('    total: int', '    total_cents: int'))
PY
# WIP is informational, even with an observed schema removal.
"$radar_binary" impact --base "$base_revision" --head WORKTREE --json
git add backend/models.py contracts/openapi.json
git commit -qm 'Rename producer summary property'
git checkout -qb consumer "$base_revision"
printf '\n// Independent consumer presentation change.\n' >> frontend/summary.ts
git add frontend/summary.ts
git commit -qm 'Adjust consumer presentation'
if "$radar_binary" scan --base "$base_revision" --branches producer,consumer --json; then
  echo "Expected committed contract conflict to fail" >&2
  exit 1
fi

# Presence verification distinguishes the compliant base from the renamed schema.
git checkout -q producer
"$radar_binary" index --json
if "$radar_binary" verify --plan plans/exports.json --ref producer --json; then
  echo "Expected missing total criterion to fail" >&2
  exit 1
fi

# Independent source changes with an unchanged schema must not invent conflicts.
git checkout -qb independent-python "$base_revision"
printf '\n# Independent worker documentation.\n' >> backend/jobs.py
git add backend/jobs.py
git commit -qm 'Document queue fixture'
git checkout -qb independent-typescript "$base_revision"
printf '\n// Independent presentation documentation.\n' >> frontend/summary.ts
git add frontend/summary.ts
git commit -qm 'Document summary fixture'
"$radar_binary" scan --base "$base_revision" --branches independent-python,independent-typescript --json
echo "Demo fixture retained at: $demo_dir"
