# Usage

The [README](../README.md) covers `radar gate`, which is what most people need.
This page covers the rest. `radar help --all` lists every command and
`radar help COMMAND` shows its flags.

## Output

Commands print a short human report. Add `--json` for machine output.

Color is used only on a terminal. `--color auto|always|never` overrides
detection, `NO_COLOR` turns it off, and `FORCE_COLOR` or `CLICOLOR_FORCE` turns
it on for pipes. Every mark (✓ ✗ ? ! ·) and word is the same with or without
color. `--json` and MCP output are never styled. On a terminal, `radar gate`
also shows a progress line on stderr and erases it before the report.

## Gate and merge-check

`radar gate` is a front end to `merge-check`. It picks the base and branches,
always proposes tests, adds branch attribution and a `next` command, and maps
`--run` to `merge-check --verify --allow-execution --suite balanced` with a
10-minute budget.

Use `merge-check` when you want exact control:

```sh
radar merge-check --base main --branches feature/api,feature/web --suggest-tests
radar merge-check --base main --branches feature/api,feature/web \
  --verify --allow-execution --suite targeted        # or balanced, full
radar merge-check --base main --branches feature/api,feature/web \
  --verify --allow-execution --cwd services/api -- python3 -m unittest test_api
```

Branches are merged in the order given, and order can change the result.
`--evidence-output .radar/evidence/candidate.json` saves execution metadata to
a new file. Nothing else is written to the repository.

`radar check --base main` analyzes one head (the working tree by default, or
`--head REF`) without combining anything: changed files, reverse import
dependencies, declared and discovered contracts, and optional plan checks.
`--suite MODE` previews test selection without running anything.

`radar discover --json` lists contract candidates and a proposed manifest. It
never edits `.radar/contracts.json`.

Selection modes and budgets are in [TEST_SELECTION](TEST_SELECTION.md).
Verdicts, policies and exit codes are in
[VERIFICATION_POLICY](VERIFICATION_POLICY.md).

## Agent setup

```sh
radar setup --agent claude          # or codex, both
radar setup --agent both --dry-run  # show the changes first
radar setup --agent claude --hook   # also install the Claude Code Stop hook (needs Bash)
```

Setup writes project-local skills and MCP configuration. It keeps unrelated
settings and refuses to overwrite a conflicting Radar entry. You may still need
to trust the project in your agent, restart it, or put `radar` on `PATH`.

`radar mcp` serves tools over stdio. Agents can call `radar_gate`,
`radar_check`, `radar_merge_check` and the read-side toolkit commands. `test`,
`approve` and every execution flag are not exposed. MCP summaries are capped at
40,000 bytes; pass `detail: true` for the full report, or use the CLI with
`--json`.

## Explicit contracts

Commit `.radar/contracts.json` next to a JSON or YAML OpenAPI document or a
JSON Schema:

```json
{
  "version": 1,
  "bindings": [{
    "id": "organization-summary",
    "schema": "openapi.yaml",
    "pointer": "/components/schemas/OrganizationSummary",
    "producer": "backend/models.py",
    "consumer": "frontend/summary.ts",
    "direction": "response",
    "fields": ["total", "owner.id"]
  }]
}
```

`radar contracts` lints the manifest and flags declared fields that no longer
appear in the consumer file (a text search, not a usage proof). `radar impact`
compares declared contracts between two revisions, and `radar scan` looks for
contract conflicts across concurrent branches. What the comparison covers is
listed in [CAPABILITIES](CAPABILITIES.md#contracts). Removing a binding does
not remove its obligation; see
[VERIFICATION_POLICY](VERIFICATION_POLICY.md#contract-obligations).

## Plans and evidence

These commands need `radar init` (or `setup`) first. Linked worktrees without
their own `.radar` share the main worktree's state, and evidence names the
repository by its root commit, so records made in one worktree verify in
another.

```sh
radar index --ref BASE_SHA
radar plan "add async exports" --ref BASE_SHA --output .radar/plans/design.json
radar preflight --plan .radar/plans/design.json --ref BASE_SHA
radar tasks --plan .radar/plans/design.json
# after an actual review:
radar approve --plan .radar/plans/design.json --reviewer NAME \
  --output .radar/plans/approved.json
radar test --plan .radar/plans/approved.json --ref IMPL_SHA \
  --allow-execution --timeout 30s -- python3 -m unittest test_export
radar verify --plan .radar/plans/approved.json --ref IMPL_SHA --evidence EVIDENCE_ID
```

- `plan` writes a context bundle and an incomplete plan. It does not design
  anything; your agent fills in the design.
- `preflight` rejects a plan whose indexed baseline has changed and prints
  next steps.
- `tasks` prints the task DAG, parallel groups and per-task packets. Tasks that
  share a component or contract are not scheduled in parallel.
- `approve` records the reviewer name you give it, bound to the plan digest.
  Editing the plan invalidates the review. It is a declaration, not
  authentication.
- `test` runs one `test_run` command from the plan on a private copy of the
  commit. The rule may declare `cwd`, `env` (variable names to pass through),
  `link` (untracked directories such as `node_modules`), `setup` commands and
  a `junit` report path (a file, or a directory whose `TEST-*.xml` files are
  all read, as Maven Surefire and Gradle write them). Radar stores the outcome
  and an output digest, not the output itself.
- `verify` checks the plan's rules against a commit and test records. Records
  from another commit or plan digest are rejected.

Plan assumptions are `open`, `accepted` or `resolved`. Open assumptions keep
verification `unknown`; resolve or accept them instead of deleting them.

Graph commands: `radar resolve NAME` finds entity IDs, `radar graph` queries the
index (`--plan approved.json --format mermaid` shows the plan's intent graph),
`radar affected --base main --head REF` follows imports back from changed
files, and `radar explain FINDING_ID` shows a stored finding.

Entity IDs look like `file:backend/models.py`,
`class:backend/models.py#ExportSummary` and
`function:svc/server.go#Server.Handle`. They do not depend on where the
repository is checked out.

## Exit codes

| Command | 0 | 1 | 2 |
| --- | --- | --- | --- |
| `gate`, or any command with `--policy` | pass | fail or blocked | error |
| `check`, `merge-check`, `scan`, `impact` without a policy | report produced, possibly with warnings or unknowns | supported failure (conflict, breaking declared contract) | invocation or analysis error |

`--require-complete` makes `check` and `merge-check` exit 1 on any unknown or
incomplete result. `--strict` makes `impact` and `scan` exit 1 when a binding
could not be analyzed. Exit 0
without a policy does not mean everything was verified; read the report.
