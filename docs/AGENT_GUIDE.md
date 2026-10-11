# Using Radar with coding agents

This page is for people who run Codex or Claude Code in parallel, and for the
agents themselves. `radar help agents` prints the short version.

## The workflow

1. **Install Radar** ([README](../README.md#install)), then run `radar doctor`
   in the repository. It shows the base branch, the agent branches Radar will
   combine, uncommitted worktrees, the test frameworks it found with whether
   their programs are on PATH, and the next step.
2. **Connect the agent:** `radar setup claude`, `radar setup codex` or
   `radar setup both` (add `--dry-run` first to see the files). This writes a
   project skill and an MCP entry. Restart the agent and approve the project
   MCP server if it asks.
3. **Start parallel work:** one branch and worktree per agent, for example
   `git worktree add -b agent-api ../agent-api main`. `radar gate` checks
   committed changes; `radar watch --include-dirty` also previews saved edits.
4. **Inspect changes:** `radar doctor` or the interactive `radar` menu lists
   which branches have commits and which worktrees still have uncommitted
   changes.
5. **Verify the combination:** `radar gate` (static, runs no code), then
   `radar gate --run` to run the related tests on the combined tree. Only a
   person authorizes `--run`. For continuous verification, use `radar watch`;
   add `--include-dirty` to include saved staged, unstaged and nonignored
   untracked files. Unsaved editor buffers and ignored files are excluded.
6. **Investigate a failure:** read `Problems`. Gate and watch default to zero
   diagnostic reruns. Opt in with `--run --attribution-runs 8` when needed.
   `observed:` lines come from rerunning the failed test on fewer branches;
   `look at` names branches and
   files to start with. Both are repair leads, not proof of blame.
7. **Repair and recheck:** fix on the branch, commit, and rerun the same
   command (the report's `Next:` line). Evidence never carries over to new
   commits. `radar watch` reruns after commits; with `--include-dirty` it also
   checks saved edits. Dirty preview evidence does not qualify committed inputs.

## What an agent may run

| Command | What it does | Runs repository code |
| --- | --- | --- |
| `radar doctor --json` | Repository facts in `project`: base, branches, dirty worktrees, test runners (program on PATH or not) and `next` | no |
| `radar gate --json` | Combine every worktree branch with commits beyond the base and check it | no |
| `radar gate A B --json` | Combine only branches `A` and `B` | no |
| MCP `radar_gate` | The same gate. Returns a bounded summary (`agent_brief`); `detail: true` returns the full report | no |
| `radar gate --run` | Run the selected tests on the combined tree | **yes: needs the user's authorization** |
| `radar watch --include-dirty --jsonl` | Stream static checks of commits and selected saved worktree snapshots | no |
| `radar watch --run` | Rerun selected tests after committed changes; add `--include-dirty` for saved edits | **yes: needs the user's authorization** |

MCP never exposes `--run`, `radar test` or `radar approve`.

## Reading a gate report

Text and JSON carry the same facts. JSON field names:

| Field | Meaning |
| --- | --- |
| `gate.verdict` | `pass`, `fail`, `blocked` or `error`; exit 0, 1, 1, 2 |
| `gate.required_checks[].status` | Only `passed` satisfies a requirement. Every other status (`failed`, `error`, `timeout`, `unknown`, `incomplete`, `blocked`, `warning`) does not |
| `next` | One suggested next step |
| `findings` (severity `error`) | What to repair, with `remediation` |
| `attribution[FINDING_ID]` | Branches and files to inspect. `basis` says whether this was observed by reruns or inferred from imports |
| `selection.blocking`, `selection.uncovered_changes` | Why tests could not cover the change |
| `worktrees[].staged/unstaged/untracked` | Uncommitted changes. Excluded by gate and default watch; selected snapshots are included with watch `--include-dirty` |
| `error`, `next` (exit 2) | Radar could not run; `next` says how to fix it |

Verdicts in the terminal:

| Shown | Meaning |
| --- | --- |
| `PASS (static)` | Merge and contract checks passed. No test ran |
| `PASS (selected policy; bounded verification)` | Required checks passed and the selected tests ran and passed. This is not full coverage |
| `FAIL` | A conflict, a breaking declared contract, or a failing test on the combined tree |
| `NOT VERIFIED` | Required evidence is missing: a runner could not start, the budget ran out, a changed file has no related test, or only documentation changed so no test ran. The report lists each gap with a `fix:` line |
| `ERROR` | A required check errored or timed out while running. Radar errors before a report (bad ref, no branches, unsupported layout) print `radar: …` and `Next:` instead, also with exit 2 |

For watch JSONL, correlate `completed.result` with `state.key`. `changed` and
`error` invalidate the current verdict; `checking` means verification is pending.
`verification_state` gives the current state. `last_completed` and `last_change`
are event timestamps, not proof an earlier result is current. Heartbeats carry no
report. Optional `attributed` events provide diagnostic leads and leave the Fast
Gate verdict unchanged. See [WATCH](WATCH.md) for snapshot limits and scheduling.

## Rules for agents

- Do not add `--run` yourself. Ask the user, or report what is unverified.
- Do not report `PASS (static)` as tested, or `NOT VERIFIED` as passing.
- Do not treat attribution as blame. Inspect the named files.
- Commit before using gate. Use watch `--include-dirty` for saved edit previews;
  a preview pass does not prove the committed branches passed.
- Do not delete contract declarations, tests or plan criteria to make a check
  pass. Contract obligations from the base survive manifest edits.
- Stop after two unsuccessful repair attempts and report the remaining finding.

## Output size

MCP responses are compact by default: verdict, required checks, findings,
attribution, a bounded test summary and `next`. Use `detail: true` or the CLI
`--json` only when you need inventories or provenance.

Planning, evidence and contract tools are described in [USAGE](USAGE.md).
