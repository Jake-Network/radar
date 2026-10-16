---
name: radar
description: Check combined parallel branches and saved worktree drift with Radar before integration, and interpret its static checks, execution evidence, and missing verification.
---

# Radar integration checks

Use the local `radar` binary at the repository root, or the matching `radar_*`
MCP tools. If unavailable, report that limitation; never invent Radar results.

Before integrating parallel branches, run `radar gate --json`. The default
combines committed worktree branches beyond the default base in private Git
state. Select a precise scope with `radar gate branch-a branch-b --base main
--json`. Dirty files are reported and excluded. Each branch passing alone does
not prove the combination passes.

Read `gate.verdict`, `checks`, `coverage`, and `limitations` separately. Only
`gate.verdict: pass` satisfies the selected policy. `blocked`, `error`, missing
checks, unsupported analysis, and unexecuted tests are NOT VERIFIED. Static PASS
means tests have not run. Follow `next`, and report the scope actually checked.
For automation inspect JSON: legacy static gate can exit zero while blocked.

`radar gate --landing --json` proposes pair relationships and landing order.
`landing.restack_first` identifies confirmed conflicts with the base: restack
those branches before checking the full combination. Excluded inputs prevent
full verification even when the remaining combination passes. Pair results do
not prove an N-way merge; repeat the full gate after repairs. This skill does
not authorize merge, rebase, private ref publication, push, or external messages.

Repository execution requires explicit user authorization. When already
provided, continue with `radar gate --run --suite targeted --json`; otherwise
request execution consent. Policies choose required checks and grant no execution
permission. Candidate directories protect the original checkout but are not an
OS sandbox. Radar does not fetch or automatically install dependencies. Declared
runner setup runs only after execution opt-in.

Inspect `runner_preparations`, `selection.blocking`, `uncovered_changes`, and
execution diagnostics when verification is incomplete. A setup/cache hit is not
test evidence. Source changes during execution invalidate candidate proof.
Failures compared with unchanged base retain observed failures; base-existing
cases are not branch repair leads. All-existing failures leave NOT VERIFIED.
An unavailable baseline cannot clear a candidate failure. Optional
`--attribution-runs N` adds bounded subset observations after execution consent;
branch attribution remains a repair lead, not proof of cause.

For saved changes, `radar watch --include-dirty --jsonl` or the native Stop hook
previews staged, unstaged, and nonignored untracked files. Unsaved buffers are
excluded. Partial peer inspection is NOT VERIFIED; a completed self preview
cannot establish peer compatibility. Hook checks are static and never execute
repository tests. Correlate watch results with `state.key`; changed/error events
invalidate the current result and optional attribution does not alter Fast Gate.

In a workspace, per-repository passes do not prove cross-repository compatibility.
Only supported declared links with `cross_repo.status: passed` establish static
link evidence; runtime interoperability remains unverified. Proposed discovery
links never become binding automatically. Inspect links, exclusions, and missing
repositories before recommending integration.

For reviewed plans, bind the baseline, plan digest, exact candidate, argv and CWD.
Use `radar preflight`, `radar tasks`, and `radar verify` as needed. Record actual
reviewer declarations only after review; never invent approval or delete
acceptance criteria to hide missing evidence. Import graphs and syntax are
inferred/structural evidence, not compiler or runtime proof.
