# Test selection modes, budgets and execution

Radar proposes verification commands from static evidence and, only with
`merge-check --verify --allow-execution`, runs them against the private
combined candidate. No mode implies complete behavioral coverage.

## Modes

| Mode | Selects | Use when |
| --- | --- | --- |
| `targeted` | Plan-declared commands; changed tests; tests reached through recorded imports or declared contracts; tests that name a changed data/schema file; Go package companions; integration-named suites when changes span package roots | Fast feedback for agents; prefer `balanced` where an uncovered change is reported |
| `balanced` (alias `recommended`) | `targeted` plus package-root fallbacks for changes without an established direct relationship | Default pre-merge verification |
| `full` | Plan-declared commands plus one whole-suite command per discovered framework and package root (`python3 -m pytest`, `go test ./...`, `cargo test`, ...) | Releases, or when targeted selection reports uncovered changes |

Preview read-only: `radar check --base main --suite targeted --json`.
Execute: `radar merge-check --base main --branches a,b --verify --allow-execution --suite targeted --policy .radar/policy.json`.

Every selected command has a `tier`. `required` covers every direct
relationship; only a package-root fallback is `optional`. Documentation-only
changes (`.md`, `.rst`, images, LICENSE) select nothing and trigger no
fallback, and a changed test file does not pull in its siblings as fallbacks.

## Grouping

Per-file commands that share a framework, working directory, tier and runner
prefix are merged into one invocation with several path arguments: pytest
(`python3 -m pytest ./a.py ./b.py`), `go test -json ./x ./y` (collapsing to
`./...` when present), Jest `--runTestsByPath`, `vitest run`, and
`node --test`. Each of these runners resolves configuration from the working
directory, so the grouped invocation observes the same files. Plan-declared
commands are exact reviewed commands and are never merged, unittest discovery
and Cargo commands are not merged, and groups are capped at 200 paths.
`grouped_from` lists the per-file candidate IDs; every executed command keeps
its own candidate-bound evidence.

Grouping changes process isolation: tests that previously ran in separate
processes share one. That matches how these suites normally run in CI, but a
suite relying on per-file process state can behave differently.

## Budgets

* `--max-commands N` (default 16, maximum 256) bounds commands **after grouping**.
  Required commands are ordered first. Commands beyond the budget are listed
  in `selection.omitted` with reason `budget_exceeded`; a required omission is
  also listed in `selection.blocking`.
* `--timeout` bounds the whole run (maximum 30 minutes). Each command gets the
  remaining time; commands not started before the deadline are recorded as
  `time_budget_exhausted`.
* Commands run sequentially. They share one private candidate worktree, so
  concurrent execution is not offered.

`integration_execution` is `incomplete` (gate `blocked`) when any required
command was omitted, not started or unrunnable, even if every executed command
passed. Observed failures take precedence, and omitted commands remain listed,
so a coverage gap is never reported as a failure and never as a pass. The
`test_selection` check passes only when nothing is blocking and no changed file
is uncovered.

## Execution environment

Before executing a suite command Radar checks, statically, that the runner can
start in the candidate: a relative runner such as `./node_modules/.bin/jest`
must exist and be executable in the candidate working directory, and a bare
tool must be on `PATH`. Untracked directories (`node_modules`, `.venv`) are
not copied into the private candidate, so such commands are reported as
`environment_unavailable` (gate `blocked`) rather than executed. Prepare the
dependencies, or declare a reviewed plan `test_run` rule with `link` to expose
them. Radar never installs dependencies: execution sets `GOPROXY=off`,
`PIP_NO_INDEX=1`, `CARGO_NET_OFFLINE=true` and `npm_config_offline=true`.

A run that fails because an import could not be resolved before any test
result appeared (`ModuleNotFoundError` in pytest collection, or
`ImportError`/`ModuleNotFoundError` under unittest) is an environment
`error`, not a test failure. `ImportError: cannot import name ...` from a
repository module stays a failure, because it usually is a regression.
Explicit `-- COMMAND` runs keep their previous behavior: they are executed and
a missing tool is reported as an execution error.

## Native selectors (Milestone C decisions)

| Engine | Decision | Reason |
| --- | --- | --- |
| Go packages | Used | `go test` per package is the native unit; Radar selects packages and groups them into one invocation. |
| Cargo | Used at manifest level | One `cargo test --manifest-path` per crate; per-test selection is not attempted. |
| Jest `--findRelatedTests`, Vitest `related` | Deferred | Both execute repository configuration (`jest.config.*`, Vite plugins) to resolve modules, so they belong only to authorized execution, and they need `node_modules`, which the private candidate lacks. A selector adapter would have to run under the same authorization and environment rules as tests. |
| Nx affected | Deferred | Requires the Nx project graph daemon/plugins (repository code) and installed dependencies. |
| pytest-testmon | Deferred | Requires a previously recorded `.testmondata` coverage database from the same environment, which a fresh candidate does not have. |

Read-only commands (`check`, MCP tools) never run a selector, test collection
hook or package script. Static recommendation, native-selector recommendation,
authorized selector execution and authorized test execution remain distinct;
only the last is implemented for native engines today.

## Agent-facing summaries

MCP `radar_check` accepts `suite` and `max_commands` for a read-only preview.
The bounded MCP summary includes an `agent_brief`: changed files, contract
impact and obligation changes, essential versus optional commands with reasons,
executed, failed and not-run commands, omissions with reasons, uncovered
changes, what must be repaired, and the required checks still unsatisfied. Full
per-file provenance stays in the JSON report (`detail=true` or `--json`).
Radar does not retry or repair; the coding agent implements repairs and reruns
the same command.
