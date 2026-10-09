# Radar pull-request integration

The workflow inspects the current PR against its exact base revision and creates
Radar's private combined candidate. It does not modify branches. Static inspection
runs by default. Runtime verification runs only after explicit repository-owner
configuration. Additional PRs participate only when their numbers are explicitly
selected; inspecting a single PR does not demonstrate multi-PR compatibility.

## Five-minute setup

Start with a prepared repository and a reviewed Radar checkout. No published
release is assumed. Copy these three files into the adopter's default branch:

```sh
mkdir -p .github/workflows .github/radar
cp /path/to/radar/integrations/github-actions/radar-pull-request.yml .github/workflows/radar.yml
cp /path/to/radar/integrations/github-actions/{run.py,report.py} .github/radar/
git -C /path/to/radar rev-parse HEAD
```

Set the `RADAR_REVISION` repository Actions variable to that **published, reviewed
40-character commit SHA** in `Jake-Network/radar`. The source revision must include
the gate fixes you intend to use; pinning an older version cannot deliver newer
fixes. A local unpublished checkout can be built for local checks but cannot be
fetched by hosted Actions. There is deliberately no moving `main`, `latest`,
placeholder release download, or fallback to an older tool version. Review helper
updates as code and commit them to your default branch before opening a PR.

The workflow builds trusted pinned Go/CGO source on an ephemeral Ubuntu runner.
Users trying locally can use a supported release binary, or build the same commit:

```sh
go build -trimpath -o /tmp/radar ./cmd/radar
/tmp/radar check --base main --json
/tmp/radar setup --agent both --dry-run
```

The integration helper generates policy in the runner's temporary directory, so
PR code cannot weaken required checks by changing a policy file:

```json
{"version":1,"require":["textual_merge","no_breaking_contracts"],"on_missing":"blocked"}
```

For local read-only candidate inspection, save that JSON as `/tmp/radar-policy.json`:

```sh
radar merge-check --base main --branches feature/api --suggest-tests \
  --policy /tmp/radar-policy.json --json
```

## Inputs and execution authorization

| Repository variable | Default | Meaning |
| --- | --- | --- |
| `RADAR_REVISION` | Required | Reviewed immutable 40-character Radar commit SHA |
| `RADAR_EXECUTE` | `false` | Set exactly `true` to authorize full-suite repository-code execution on same-repository PR heads |
| `RADAR_ADDITIONAL_PRS` | Empty | Comma-separated explicitly selected PR numbers, at most eight; fetched as immutable commits for the combined candidate |
| `RADAR_TRUST_ADDITIONAL` | `false` | Set exactly `true` only after reviewing and authorizing execution of every selected additional PR head |

Runtime policy additionally requires `test_selection` and `integration_execution`.
Missing runners, omitted required commands, unsupported tests or absent observations
cannot satisfy it. Static reports explicitly state that execution is unavailable;
a static pass establishes only the required static checks.

Before enabling execution, review repository tests and runner privileges. Provision
required Python/Node runners and offline dependencies through trusted, pinned
workflow steps, never through a script from the PR. The example installs no target
repository dependencies and may therefore correctly block projects needing them.
For a local authorized combined test, use:

```sh
radar merge-check --base main --branches feature/api,feature/web \
  --verify --allow-execution --suite full --json
```

The example rejects execution requests for the current fork PR. Additional PR
heads can themselves originate from forks, so combined execution is blocked by
default. To authorize a reviewed combined candidate, select the PR numbers and set
`RADAR_TRUST_ADDITIONAL=true` together with `RADAR_EXECUTE=true`. This is explicit
owner consent to execute **every selected head**, including fork-origin heads, with
the runner's privileges. PR numbers refer to mutable heads: review their latest
revisions and revoke consent when that trust no longer applies. The driver resolves
and records exact fetched commit SHAs for each run. No trust is inferred from a
passing branch test or PR metadata. Static additional-PR inspection remains
available without execution consent.

## Feedback and automation

The job records `radar-report.json` as an artifact and renders escaped, bounded
GitHub annotations and a job summary. Feedback includes the required verdict,
contract findings, source locations, affected files, selected commands and reasons,
omissions, failed cases when supplied, and next actions. JSON remains Radar's gate
report, including checks, coverage and candidate provenance; authorization/setup
failures are structured blocked/error reports rather than fabricated observations.

Exit `0` means every required policy check passed. Exit `1` means failed or blocked:
failed identifies observed incompatibility; blocked identifies missing required
evidence. Exit `2` identifies an execution/analysis environment error. A failing
verification step remains a failing job even though rendering/upload use `always()`.

## Trust boundary

Use `pull_request`, never `pull_request_target` for test execution. The token has
only `contents: read`; checkout credentials are not persisted. No repository secrets
or token environment variables are supplied to tests. Radar's private candidate is
**not a security sandbox**. Execution can access the ephemeral runner, network and
available files. Same-repository origin does not prove code is safe; enabling
`RADAR_EXECUTE` is owner consent to that risk for all such PRs. Do not use persistent
self-hosted runners for this example.

Branch metadata, paths, report strings and repository configurations are untrusted.
The helpers use subprocess argument arrays, validate PR numbers and commit SHAs,
produce their own policy, and escape annotation control characters. They do not
approve, merge, push, install target packages or modify contributor branches.

Codex and Claude Code can run Radar's read-only MCP/skills alongside this workflow.
`radar setup --agent both` configures those integrations; it does not authorize
execution, and MCP cannot run arbitrary tests or fabricate review approval.

Local helper tests and YAML checks are reproducible. Hosted Actions execution and
release availability must be validated separately; local checks are not hosted CI
results. See [security](../../docs/SECURITY.md) and
[verification policy](../../docs/VERIFICATION_POLICY.md).
