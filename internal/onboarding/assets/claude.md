---
name: radar
description: Check that parallel agent branches integrate before merging (radar_gate), and ground feature design, multi-agent task splits and implementation checks in Radar repository evidence. Use for changes that touch shared API contracts, span several modules or agents, or need a reviewed plan and verifiable acceptance criteria.
---

# Radar workflow for Claude Code

Radar is a local CLI (and MCP server) that indexes the repository, checks
structured plans against it, and verifies implementations. Use the `radar_*`
MCP tools when they are available; otherwise run the `radar` binary from the
repository root with `--json`. If neither is available, say so; never invent
Radar output.

## 0. Before merging parallel work: `radar_gate`

When several agents or worktrees changed the repository, call `radar_gate`
(or `radar gate --json`) before proposing a merge. With no arguments it
combines every worktree branch that has commits beyond the base, in private Git
state, and reports conflicts, breaking contract changes and the branches whose
files each finding points at (`attribution`). It never runs repository code.
Its `next` field is the command to run next; `radar gate --run` (tests on the
combined tree) executes repository code, so ask the user before running it.
Repair findings on the branch named in `attribution`, commit, and rerun the
gate; results for individual branches never prove the combination works.

In a workspace (`radar workspace add PATH` groups repositories on this
machine), the same call checks every workspace repository, each combined on
its own. Pass `targets` (for example `orders:agent/api,payments:agent/client`)
to name branches in several repositories; unnamed repositories take part at
their base. Declared workspace links compare producer schemas with consumer
fields across base/candidate combinations. Only `cross_repo.status: passed`
is evidence that the declared, supported static links passed; per-repository
passes alone do not establish cross-repository compatibility. Check `links`
for incomplete inputs and intermediate-cell failures. Runtime behavior and
rollout safety remain unverified by these static checks. `suggested_links` are
proposals and never affect the verdict. To declare one, use the CLI
`radar workspace connect PRODUCER CONSUMER_REPO --fields a,b --direction response`
(with `request` for request contracts and optional `--source PATH` / `--into repo:PATH`),
then commit the home repository's `.radar/workspace.json` and the consumer's
`.radar/consumes.json`. Gate reads the committed home base declaration; replay
pins the recorded base and team blob. `.radar/contracts.json` describes links
within a repository; workspace links cross repositories. MCP does not expose
`connect`.

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
- `test_run` rules declare exact argv and optional repository-relative `cwd`. Add `setup` steps, `env` names, `link`
  paths (e.g. `node_modules`, `.venv`) or a `junit` report path when the test
  needs them. Candidate integration does not support host dependency links; use
  reviewed setup inside private state when dependencies are needed.
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

Start a repository without manual bindings with `radar check --base BASE --suggest-tests --json`.
Use `radar discover --json` for evidence-backed proposed relationships; review
uncertainties before explicitly accepting any manifest. Lexical similarity is
not runtime proof. MCP equivalents are `radar_check` and `radar_contracts_discover`.
For `radar_check`/`radar_merge_check`, use `suggest_tests: true` and optional
`policy` to inspect the bounded default summary. Request `detail: true` only
when omitted findings, commands or inventory are needed; full responses are
still capped at 40,000 bytes. Use CLI JSON when the complete local report is needed.

Before integrating concurrent branches, run
`radar merge-check --base BASE --branches A,B --suggest-tests --json` (or `radar_merge_check`).
This previews combined source using Git semantics. It does not merge user branches
or execute tests. Independent branch tests are never evidence for the combination.
Inspect `verification_proposal`: argv, CWD, priority, affected files and evidence
reasons. `declared_contract_impact` can follow explicitly declared producer/
consumer links into consumer imports/tests; inspect the manifest locations.
Discovered/proposed links cannot establish that relationship, and runtime usage
remains unproved. Recommendations are inferred/proposed and do not install dependencies
or establish complete test coverage. Define a versioned `--policy PATH` selecting
required checks; distinguish `gate.verdict` from aggregate status and coverage.
Missing required evidence remains blocked; unrelated analyzer gaps stay visible.
The base `.radar/contracts.json` defines contract obligations: deleting the
manifest, a binding or a consumed field declaration never makes
`no_breaking_contracts` pass. Retire an obligation only with a reviewed
`"retired": [{"id", "fields", "reason"}]` entry (or an approved plan removal
delta); never remove declarations to silence a finding.

After explicit execution authorization, use the CLI:
`radar merge-check --base BASE --branches A,B --verify --suite targeted
--allow-execution --policy POLICY --json`. Modes: `targeted` (direct static
relationships), `balanced` (alias `recommended`; adds package fallbacks) and
`full` (whole discovered suites). Preview any mode read-only with
`radar check --suite MODE`. Supply a reviewed plan declaring exact test commands
when checking acceptance criteria. Keep selected commands separate from executed
observations; inspect every result and its CWD. Same-directory per-file commands
are grouped; `--max-commands` (default 16) and `--timeout` bound one run. Read
`selection.omitted`, `uncovered_changes` and `blocking`: an omitted, unrunnable or
unexecuted required command blocks the gate and is never a pass or a test failure.
Radar never installs dependencies; an `environment_unavailable` result needs a
prepared environment or a reviewed plan `test_run` rule with `link`. A command observation does not automatically satisfy
an unrelated plan criterion. Private copied files are not an OS sandbox.
MCP is strictly read-only for proposals and preview: never pass execution flags or
attempt to create approval through a tool call. Consult Radar CLI help and its
intelligent-verification guide for the supported scope.

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
