---
name: radar
description: Ground feature design, multi-agent task splits and implementation checks in Radar repository evidence. Use for changes that touch shared API contracts, span several modules or agents, or need a reviewed plan and verifiable acceptance criteria.
---

# Radar workflow for Claude Code

Radar is a local CLI (and MCP server) that indexes the repository, checks
structured plans against it, and verifies implementations. Use the `radar_*`
MCP tools when they are available; otherwise run the `radar` binary from the
repository root with `--json`. If neither is available, say so; never invent
Radar output.

## 1. Ground the design

1. `radar_doctor` (or `radar doctor`). If `initialized` is false, run `radar_init` once.
2. Pick a committed baseline SHA and run `radar_index` with `ref` set to it.
3. `radar_plan` with the feature intent and the same `ref`. Read the context
   file it writes: lexical matches plus files that import or are imported by
   them (`DEPENDS_ON` edges are inferred from import paths, not compiler-verified).
4. Investigate the actual source before designing. Use `radar_resolve` to turn
   names or `path#Qualified.Name` into entity IDs and `radar_graph` with
   `from`, `edge: DEPENDS_ON`, `reverse: true` to find dependents.

## 2. Fill and check the plan

Edit the generated plan JSON: requirements, decisions with alternatives and
tradeoffs, tasks (`components` are entity IDs, `contracts` are binding IDs from
`.radar/contracts.json`), `depends_on`, acceptance criteria with deterministic
`rule`s, and assumptions.

- Assumptions are objects: `{"id", "text", "status", "resolution"}`. Leave them
  `open` until investigated. Mark `resolved` (with evidence) or `accepted` (with
  who accepts the risk and why) only when that is true. Never delete an
  assumption to make verification pass.
- `test_run` rules declare exact argv. Add `setup` steps, `env` names, `link`
  paths (e.g. `node_modules`, `.venv`) or a `junit` report path when the test
  needs them.
- Empty `incomplete` only when the design is actually complete.

Run `radar_preflight` and follow its `next_steps` until no errors remain.
`radar_contracts` lints the contract manifest and flags stale consumer fields.

## 3. Review and split work

Present consequential decisions to the user. Only after a human has reviewed
the plan may the user run `radar approve --plan PATH --reviewer NAME`; never run
it on their behalf or invent a reviewer. `radar_tasks` returns the DAG, parallel
groups and self-contained instruction packets for parallel agents or worktrees.
Linked Git worktrees share the main checkout's Radar state and entity IDs, so
evidence recorded in one worktree verifies in another.

## 4. Check what shipped

- `radar_affected` (base = baseline, head = WORKTREE or a commit) lists files,
  contracts and plan tasks reached through import dependencies.
- `radar_impact` / `radar_scan` compare declared contracts at committed
  checkpoints. A status of `incomplete` means some bindings were not analyzed;
  that is not a pass.
- Test execution runs repository code, so it needs the user's explicit opt-in:
  ask them to run `radar test --plan PATH --ref SHA --allow-execution -- ARGV`.
  A status of `error` means the command failed before any test ran (missing
  dependency, build or setup failure), not that tests failed.
- `radar_verify` with `ref` and `evidence` IDs. Report passed, failed, unknown
  and blocked checks exactly as returned. Working-tree verification is
  informational.

This skill does not authorize merges, rebases, resets, pushes or external messages.


## Combined integration and bounded repair

Start a repository without manual bindings with `radar check --base BASE --json`.
Use `radar discover --json` for evidence-backed proposed relationships; review
uncertainties before explicitly accepting any manifest. Lexical similarity is
not runtime proof. MCP equivalents are `radar_check` and `radar_contracts_discover`.

Before integrating concurrent branches, run
`radar merge-check --base BASE --branches A,B --json` (or `radar_merge_check`).
This previews combined source using Git semantics. It does not merge user branches
or execute tests. Independent branch tests are never evidence for the combination.
After explicit execution authorization, use the CLI with `--verify --allow-execution`
and a plan declaring exact test commands. Filesystem isolation is not a secure sandbox.

Consume finding IDs, evidence classes, source locations, remediation and required
verification. Repair only a supported invariant, then re-check the exact repaired
state. Stop after two unsuccessful repair attempts and report the remaining blocker.
If the same finding ID recurs without new evidence, investigate the cause before
repeating an edit; do not loop or delete criteria to suppress feedback.

For parallel work, give shared components one owner or explicit ordering. Add a
shared integration test criterion for cross-component behavior. Context includes
one-hop import and declared contract neighbors; inspect provenance and source.
Radar cannot infer contradictions in free-form design prose or establish runtime
architecture from static context. Consequential alternatives and tradeoffs still
need agent reasoning and actual human review; no MCP tool can supply that approval.
