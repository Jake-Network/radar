# Using Radar with coding agents

Install Radar once, then connect it to Claude Code or Codex. Native Stop hooks
preview saved work before a response ends; skills and MCP support investigation.
The hook performs static checks, not tests or automatic merge approval.

## Project-local installation

Install the CLI ([README](../README.md#install)) and make `radar` and Git available
on the agent process's `PATH`. Run these commands in the project to check:

```sh
radar setup claude
radar setup codex
radar setup both
```

Setup installs a native Stop hook, project skill and MCP entry. Use `--dry-run`
to inspect proposed files. Restart the agent and approve the project MCP server
if prompted. In Codex, trust the project and review the Radar hook with `/hooks`;
installation does not grant hook trust, and changed definitions require review
again. Check setup output for activation steps. `--hook=false` skips hook
installation; it does not remove existing hooks.

The hook defaults to **current `HEAD` only**, including its saved staged,
unstaged and nonignored untracked changes. Explicitly select parallel scope:

```sh
radar setup both --base main --branches api,client
```

Selected branches with available worktrees also contribute saved dirty changes.
Unsaved editor buffers and ignored files are excluded. A dirty preview does not
qualify committed integration inputs. Unlike the legacy plan hook, the native
Stop hook requires no active plan.

## Portable native plugins

Teams can copy the hook-only packages in `integrations/plugins/claude` and
`integrations/plugins/codex`. They contain hook definitions, not a Radar binary
or dependency installer. From a Radar source checkout:

```sh
claude plugin marketplace add /absolute/path/to/radar/integrations/plugins
claude plugin install radar@radar-hooks

codex plugin marketplace add /absolute/path/to/radar/integrations/plugins
codex plugin add radar@radar-hooks
```

Both marketplace catalogs are named `radar-hooks` and select their matching
adapter. Keep the local marketplace directory available for refreshes. Start a
new session and inspect installed hooks; Codex still requires `/hooks` trust
review. Do not install a native plugin and a project-local Radar hook for the
same agent: merged hook sources could run both. Claude also supports one-session
loading with `claude --plugin-dir /absolute/path/to/radar/integrations/plugins/claude`.

Plugin commands require an agent version supporting plugins and Stop hooks.
These packages target local agent execution; Codex IDE and cloud support differs.
OpenAI's public plugin directory currently does not accept lifecycle-hook
packages. Packaging follows [Claude plugins](https://code.claude.com/docs/en/plugins-reference),
[Claude marketplaces](https://code.claude.com/docs/en/plugin-marketplaces),
[OpenAI plugin packaging](https://developers.openai.com/plugins/build/plugins)
and [Codex hooks](https://learn.chatgpt.com/docs/hooks).

## Hook behavior and repair

`Stop` is a response ending, not feature completion. The native commands are:

```sh
radar hook stop --agent claude --timeout 20s
radar hook stop --agent codex --timeout 20s
```

For custom team plugins, add `--base main --branches api,client`. Keep the outer
agent timeout longer than Radar's timeout (packages use 25 seconds and 20 seconds).
The hook builds a private saved-change candidate. It runs no repository tests,
hooks or dependency installation, fetches nothing and changes no source branch.

Supported static failures return Stop JSON requesting investigation and repair.
Committed textual conflicts produce an informational `systemMessage`: restack or
reconcile those branches before landing. New conflicted paths introduced by saved
changes still block. Other supported failures, including committed migration
heads, remain repairable in the current branch and still block.

Unchanged previews reuse the last result for that worktree. The key includes
HEAD, base and selected commits, saved content and index digests, configuration,
Radar version and executable identity. Changes invalidate it; malformed cache
records are ignored. Cache files use the existing run/coverage state directory
(`RADAR_STATE_DIR`, otherwise the user cache directory's `radar/hook-previews`),
so an uninitialized checkout does not acquire untracked cache inputs. Cached
previews remain static checks and never count as test evidence.
One hook-induced continuation is allowed: when `stop_hook_active` is true, Radar
skips the check to prevent a loop. **After repair, explicitly rerun Radar**;
the continuation does not automatically validate the fix. For saved previews,
use `radar watch --include-dirty` with the same base and branch scope;
after committing, use `radar gate` with that scope.

Unknown or incomplete results remain informational. A successful static check
does not prove runtime compatibility; final reports must list remaining gaps.
Tests require the user's authorization for `--run`, even when a hook is installed.

## Combining parallel work

Use one branch/worktree per agent, then check the exact branches to combine:

```sh
radar doctor
radar gate api client --base main
radar gate api client --base main --run
```

The last command executes selected tests and needs authorization. Unlike the
hook's current-HEAD default, `radar gate` without branch arguments selects all
worktree branches with commits beyond the base. Gate excludes dirty files.
For continuous saved previews, see [watch](USAGE.md#watch); a preview result
cannot substitute for verification of final commits.

When a finding appears, inspect its remediation and named files, repair, commit
and rerun the same command. Attribution is an investigation lead, not proof of
blame. Failed commands automatically receive one budgeted base comparison; base-existing
failures leave verification incomplete and have no branch lead. Optional subset
diagnostic reruns default to zero; opt in with `--run --attribution-runs 8`.
Never remove tests, contracts or plan criteria to make a result pass.

## Reading results and agent access

| Result | Meaning |
| --- | --- |
| `PASS (static)` | Supported merge/contract checks passed; no tests ran |
| Bounded verification PASS | Selected policy requirements passed; not full coverage |
| `FAIL` | Merge conflict, breaking declared contract or observed test failure |
| `NOT VERIFIED` | Required evidence missing, including unavailable runners or uncovered changes |
| `ERROR` | Analysis/execution could not complete; inspect the reported next step |

`gate.verdict` is separate from coverage and individual check status. Only
`passed` satisfies a required check. Gate exits 0 for pass, 1 for fail/blocked,
and 2 for error. See [verification](VERIFICATION.md) for policies, test selection
and evidence limits.

Agents may run `radar doctor --json`, `radar gate --json` and static watch without
executing repository code. MCP `radar_gate` returns a bounded `agent_brief`;
use `detail: true` for the full report. MCP exposes no `--run`, `radar test` or
`radar approve`. Never describe a static result as tested or missing evidence as
passing. Plan, contract and evidence commands are in [USAGE](USAGE.md).

## Validate or remove

```sh
python3 -m unittest discover -s integrations/plugins -p 'test_*.py'
claude plugin validate integrations/plugins/claude
claude plugin validate integrations/plugins
python3 integrations/plugins/smoke_native.py --radar /absolute/path/to/radar

claude plugin uninstall radar@radar-hooks
codex plugin remove radar@radar-hooks
```

For project-local removal, delete only Radar's Stop handler from
`.claude/settings.json` or `.codex/hooks.json`, preserving other handlers and
settings. Native plugins can also be disabled in the agent's plugin browser.
Package tests validate wiring; `scripts/agent-hooks-test.py` exercises hook
behavior against a built binary. The opt-in native smoke needs both agent CLIs,
uses temporary configuration homes and checks the continuation guard. These
checks do not prove an authenticated model session or activation after trust
review.
