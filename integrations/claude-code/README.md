# Claude Code integration

Run `radar setup --agent claude` for project-local skill and MCP configuration, or add `--hook` for the optional Stop hook. `--dry-run` shows changes. Existing unrelated settings are preserved; conflicting Radar entries require manual reconciliation. Put Radar on PATH, approve the project MCP configuration if prompted, and restart Claude Code. No API key is needed by Radar.

The three pieces can also be installed manually below.

## 1. MCP server: Radar as agent tools

`radar mcp` serves Radar over the Model Context Protocol (stdio). Register it
for a project:

```sh
claude mcp add radar -- radar mcp --root .
# or commit the example as .mcp.json:
cp /path/to/radar/integrations/claude-code/mcp.example.json .mcp.json
```

Tools: `radar_doctor`, `radar_init`, `radar_index`, `radar_resolve`,
`radar_graph`, `radar_plan`, `radar_preflight`, `radar_tasks`, `radar_verify`,
`radar_impact`, `radar_scan`, `radar_affected`, `radar_contracts`,
`radar_explain`, `radar_check`, `radar_contracts_discover`, `radar_merge_check`. `radar test` (runs repository code) and `radar approve`
(declares a human review) are deliberately not exposed; the user runs them.

## 2. Skill: the planning and verification workflow

```sh
mkdir -p .claude/skills/radar
cp /path/to/radar/integrations/claude-code/skills/radar/SKILL.md .claude/skills/radar/
```

## 3. Stop hook: verify before the agent finishes

```sh
mkdir -p .claude/hooks
cp /path/to/radar/integrations/claude-code/hooks/radar-verify-stop.sh .claude/hooks/
```

Merge `settings.example.json` into `.claude/settings.json`, then select the
plan to enforce:

```sh
echo .radar/plans/my-feature.approved.json > .radar/active-plan   # or export RADAR_PLAN=...
```

When the agent tries to stop, the hook runs `radar verify` on the working tree.
If a supported check fails (exit 1), the hook exits 2 and Claude Code returns
the failed checks to the agent. Unknown results, missing plans or a missing
`radar` binary never block. Working-tree verification is informational; record
committed verification with `radar verify --ref SHA` as usual.

The combined preview tool accepts only analysis inputs; `verify` and execution authorization are rejected by MCP. The CLI supports explicit authorized combined verification. The Stop hook returns JSON finding/task feedback on failure and skips repeated Stop-hook repair attempts; it does not run integration tests or approve a plan.
