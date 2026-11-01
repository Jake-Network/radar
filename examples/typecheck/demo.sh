#!/usr/bin/env bash
# One agent renames a struct field and updates its uses; another adds code
# that still reads the old field. Each branch type-checks and tests green.
# Static checks do not resolve field access, so the static gate passes the
# combination; `radar gate --run --suite full --typecheck` type-checks the combined tree
# and names the line. A Python project shows the same with mypy, when
# python3 -m mypy and ruff are installed. Usage: demo.sh [PATH_TO_RADAR].
# Requires Git and Go.
set -euo pipefail
command -v go >/dev/null || { echo "typecheck demo: go is required" >&2; exit 1; }
workspace=$(mktemp -d "${TMPDIR:-/tmp}/radar-typecheck.XXXXXX")
trap 'rm -rf "$workspace"' EXIT
if [[ -n "${1:-${RADAR_BIN:-}}" ]]; then
  radar=$(cd "$(dirname "${1:-$RADAR_BIN}")" && pwd)/$(basename "${1:-$RADAR_BIN}")
else
  radar="$workspace/radar"
  (cd "$(dirname "$0")/../.." && go build -o "$radar" ./cmd/radar)
fi
export NO_COLOR=1 RADAR_CONFIG_DIR="$workspace/config" RADAR_STATE_DIR="$workspace/state"
fail() { printf 'typecheck demo: %s\n' "$1" >&2; exit 1; }
expect() { # expect FILE TEXT
  grep -qF -- "$2" "$1" || { cat "$1" >&2; fail "expected: $2"; }
}

repo="$workspace/accounts"
git init -q -b main "$repo"
cd "$repo"
git config user.name demo
git config user.email demo@radar.invalid
git config core.hooksPath /dev/null
mkdir -p user report
printf 'module example.com/accounts\n\ngo 1.21\n' > go.mod
printf 'package user\n\ntype User struct{ Name string }\n\nfunc Greeting(u User) string { return "hi " + u.Name }\n' > user/user.go
printf 'package user\n\nimport "testing"\n\nfunc TestGreeting(t *testing.T) {\n\tif Greeting(User{Name: "a"}) != "hi a" {\n\t\tt.Fatal()\n\t}\n}\n' > user/user_test.go
printf 'package report\n\nimport "testing"\n\nfunc TestNothing(t *testing.T) {}\n' > report/report_test.go
git add . && git commit -qm baseline
git worktree add -q -b agent-rename ../rename
git worktree add -q -b agent-report ../report
# Agent 1 renames the field and updates every use it can see.
printf 'package user\n\ntype User struct{ FullName string }\n\nfunc Greeting(u User) string { return "hi " + u.FullName }\n' > ../rename/user/user.go
printf 'package user\n\nimport "testing"\n\nfunc TestGreeting(t *testing.T) {\n\tif Greeting(User{FullName: "a"}) != "hi a" {\n\t\tt.Fatal()\n\t}\n}\n' > ../rename/user/user_test.go
git -C ../rename commit -qam 'Rename User.Name to FullName'
# Agent 2 adds a report that reads the old field.
printf 'package report\n\nimport "example.com/accounts/user"\n\nfunc Line(u user.User) string { return "user: " + u.Name }\n' > ../report/report/report.go
git -C ../report add . && git -C ../report commit -qm 'Add user report line'

for branch in agent-rename agent-report; do
  "$radar" gate --run --suite full --typecheck "$branch" > "$workspace/alone.txt" || { cat "$workspace/alone.txt" >&2; fail "$branch should pass alone"; }
  expect "$workspace/alone.txt" "✓ Typecheck"
done

"$radar" gate > "$workspace/static.txt" || { cat "$workspace/static.txt" >&2; fail "the static gate cannot see field access and should not fail"; }

if "$radar" gate --run --suite full --typecheck > "$workspace/combined.txt"; then
  fail "the combined branches should fail type checking"
fi
expect "$workspace/combined.txt" "✗ Typecheck"
expect "$workspace/combined.txt" "1 new type error not on base"
expect "$workspace/combined.txt" "report/report.go:5"
"$radar" gate --run --suite full --typecheck --json > "$workspace/combined.json" || true
python3 - "$workspace/combined.json" <<'PY' || fail "JSON should require and fail combined_typecheck"
import json, sys
r = json.load(open(sys.argv[1]))
assert "combined_typecheck" in r["gate"]["policy"]["require"]
assert any(c["id"] == "combined_typecheck" and c["status"] == "failed" and c["evidence"] == "verified_tool" for c in r["checks"])
[ev] = r["typecheck_executions"]
assert [d["path"] for d in ev["new_diagnostics"]] == ["report/report.go"]
PY

# Repair: the report reads the renamed field.
printf 'package report\n\nimport "example.com/accounts/user"\n\nfunc Line(u user.User) string { return "user: " + u.FullName }\n' > ../report/report/report.go
git -C ../report commit -qam 'Read FullName'
"$radar" gate --run --suite full --typecheck > "$workspace/repaired.txt" || { cat "$workspace/repaired.txt" >&2; fail "the repaired combination should pass"; }
expect "$workspace/repaired.txt" "no new type errors in the checked modules"

# The same rename in a Python project configured for mypy and ruff.
if ! python3 -m mypy --version >/dev/null 2>&1 || ! command -v ruff >/dev/null; then
  echo "SKIPPED: python3 -m mypy or ruff is not installed; the Python part did not run."
  echo "typecheck demo: ok"
  exit 0
fi
py="$workspace/billing"
git init -q -b main "$py"
cd "$py"
git config user.name demo
git config user.email demo@radar.invalid
git config core.hooksPath /dev/null
mkdir -p app tests
printf '[project]\nname = "billing"\nversion = "0"\n\n[tool.mypy]\n\n[tool.ruff]\n' > pyproject.toml
: > app/__init__.py
printf 'class User:\n    def __init__(self, name: str) -> None:\n        self.name = name\n' > app/models.py
printf 'import unittest\n\nfrom app.models import User\n\n\nclass UserTest(unittest.TestCase):\n    def test_name(self) -> None:\n        self.assertTrue(User("a"))\n' > tests/test_models.py
git add . && git commit -qm baseline
git worktree add -q -b agent-rename ../py-rename
git worktree add -q -b agent-report ../py-report
printf 'class User:\n    def __init__(self, full_name: str) -> None:\n        self.full_name = full_name\n' > ../py-rename/app/models.py
git -C ../py-rename commit -qam 'Rename User.name to full_name'
printf 'from app.models import User\n\n\ndef line(u: User) -> str:\n    return "user: " + u.name\n' > ../py-report/app/report.py
git -C ../py-report add . && git -C ../py-report commit -qm 'Add user report line'
for branch in agent-rename agent-report; do
  "$radar" gate --run --suite full --typecheck "$branch" > "$workspace/py-alone.txt" || true
  expect "$workspace/py-alone.txt" "✓ Typecheck"
done
if "$radar" gate --run --suite full --typecheck > "$workspace/py-combined.txt"; then
  fail "the combined Python branches should fail type checking"
fi
expect "$workspace/py-combined.txt" "✗ Typecheck"
expect "$workspace/py-combined.txt" "app/report.py:5  uses User.name, which the combined source does not define"
echo "typecheck demo: ok"
