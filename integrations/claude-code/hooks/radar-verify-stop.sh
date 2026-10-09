#!/usr/bin/env bash
# Claude Code Stop hook: before the agent finishes, verify the working tree
# against the active Radar plan and hand failed checks back to the agent.
#
# The active plan comes from $RADAR_PLAN or the first line of
# .radar/active-plan. Without one, or without radar on PATH, the hook does
# nothing. Only a supported failed check (radar exit 1) blocks; unknown or
# informational results never do.
set -uo pipefail

input=$(cat)
# Claude Code sets stop_hook_active after a Stop hook already blocked once.
if printf '%s' "$input" | grep -Eq '"stop_hook_active"[[:space:]]*:[[:space:]]*true'; then
  exit 0
fi

project=${CLAUDE_PROJECT_DIR:-$(pwd)}
plan=${RADAR_PLAN:-}
if [[ -z "$plan" && -f "$project/.radar/active-plan" ]]; then
  plan=$(head -n 1 "$project/.radar/active-plan")
fi
[[ -n "$plan" ]] || exit 0
[[ "$plan" == /* ]] || plan="$project/$plan"
[[ -f "$plan" ]] || exit 0
if ! command -v radar >/dev/null 2>&1; then
  echo "radar is not on PATH; skipped plan verification" >&2
  exit 0
fi

report=$(radar verify --plan "$plan" --root "$project" 2>&1)
status=$?
if [[ $status -eq 1 ]]; then
  {
    echo "Radar verification of the working tree against $plan reports failed checks:"
    echo "$report"
    echo "Fix the failed checks, or tell the user why they cannot pass yet."
  } >&2
  exit 2
fi
exit 0
