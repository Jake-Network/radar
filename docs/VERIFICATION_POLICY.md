# Verdicts and policies

## Three separate answers

Every `gate`, `check` and `merge-check` report carries three things that are
easy to mix up:

- `gate.verdict`: `pass`, `fail`, `blocked` or `error`. This answers "did the
  required checks pass?"
- `coverage`: what the analysis covered, what it skipped and why.
- `status` and `checks`: the per-check results, each with its evidence
  category (`verified_static`, `verified_tool`, `observed_test`, `inferred`,
  `proposed`, `unknown`).

A gate can pass while coverage is incomplete. Read both.

| Verdict | Exit | Meaning |
| --- | --- | --- |
| `pass`, static | 0 | Required static checks passed. No tests ran. |
| `pass` with `--run` | 0 | Required checks passed on the committed combined tree. |
| `blocked` | 1 | Required evidence is missing, incomplete or unsupported, or a runner was unavailable. |
| `fail` | 1 | A required check found a problem, or `on_missing: fail` turned missing evidence into a failure. |
| `error` | 2 | Bad invocation, analysis error, or an execution error or timeout. |

The terminal shows `PASS`, `PASS (static)`, `FAIL`, `NOT VERIFIED` (blocked)
and `ERROR`.

## Default gate requirements

`radar gate` requires `textual_merge` and `no_breaking_contracts`. With
`--run` it also requires `integration_execution` and `test_selection`, so a
passing test cannot make up for changed files that no selected test relates
to, an incomplete inventory, or required commands that were skipped. This will
block some changes whose tests read files at runtime or use dynamic imports.
Check the listed paths, or try `--suite full`.

Without `--run` or a policy, missing test evidence alone does not fail the
gate. The report says the tests were not run.

When no contract is declared and the base has no contract obligations,
`no_breaking_contracts` passes trivially. The terminal shows it as
`· Contracts none declared` rather than a check mark. JSON is unchanged.

## Policies

A policy is a small JSON file that names the checks you require. It cannot
grant permission to run code.

```json
{"version":1,"name":"integration","require":["textual_merge","no_breaking_contracts","integration_execution","test_selection"],"on_missing":"blocked"}
```

```sh
radar gate --run --policy .radar/integration-policy.json feature/api feature/web
radar check --base main --policy .radar/analysis-policy.json
```

Check IDs:

| ID | Passes when |
| --- | --- |
| `textual_merge` | branches merge without conflicts |
| `no_breaking_contracts` | no supported breaking change against declared or base contracts |
| `declared_contracts` | an accepted manifest exists and every binding was analyzed |
| `discovered_contracts` | discovered candidates show no supported break |
| `dependency_impact` | reverse import analysis completed |
| `source_stability` | the analyzed working tree did not change during the run |
| `configuration_stability` | the selected plan and policy files did not change during the run |
| `integration_execution` | every required test command ran and passed on the combined tree |
| `test_selection` | no changed file is uncovered and nothing required was omitted |
| `plan_verification` | the whole plan verified on the candidate |
| `criterion:ID` | one plan acceptance criterion verified on the candidate |
| `test:SELECTION_ID` | one selected test command passed |

An unknown ID counts as missing evidence. `on_missing` is `blocked` (default)
or `fail`. A warning, `unknown` or `incomplete` result never satisfies a
required check. Problems in checks you did not require stay visible in the
report without affecting the verdict.

With `--policy`, every command uses the gate exit codes (0, 1, 2). Without one,
`check` and `merge-check` keep their older exit behavior. `--require-complete`
is the older all-or-nothing switch and cannot be combined with `--policy`.

A limited policy that leaves out `test_selection` can pass with uncovered
changes. The terminal labels such a pass, and the JSON keeps every gap.

## Contract obligations

The base revision's `.radar/contracts.json` defines the obligations under
review. A branch can add bindings. It cannot drop them by deleting the
manifest, removing or narrowing a binding, clearing its consumer, or pointing
it somewhere else. Radar checks every schema change against both the base and
the head declarations.

| Base binding in the head | Schema change | Result |
| --- | --- | --- |
| unchanged | breaking | fail (`contract_<kind>`) |
| removed, narrowed or moved, consumer file still there | breaking | fail (`contract_obligation_removed`) |
| removed, consumer file still there | none | blocked |
| removed together with its consumer file | any | pass, listed as `consumer_removed` |
| retired with a reason | breaking | pass, with a `contract_obligation_retired` warning |
| head manifest unparsable, or retirement without a reason | any | blocked (`check`) or error (`merge-check`) |
| moved to a schema or pointer Radar cannot analyze, or consumer file missing | any | blocked |

To retire an obligation, add a `retired` entry in the same change:

```json
{"version": 1, "bindings": [],
 "retired": [{"id": "order-summary", "fields": ["currency"],
              "reason": "storefront reads currency_code from release 4.2"}]}
```

Leave out `fields` to retire the whole binding. An approved plan whose
`contract_deltas` contain `{"operation": "remove", "contract": ID}` also
retires it. A retirement that was already in the base does not apply again.
Radar cannot tell who reviewed a retirement; protect `.radar/` with code owners
if that matters.

## Committed inputs only

The combined candidate is built from commits. Staged, unstaged and untracked
files are left out and listed under `worktrees` in the report, along with
detached worktrees. Commit what you want checked and rerun. A detached worktree
commit has to be named by SHA.

`--plan` and `--policy` files are read once. Radar records their SHA-256 in
`configuration_inputs`, reads them again after the run, and marks
`configuration_stability` incomplete if they changed. That invalidates any pass
that depended on them, even under a policy that does not require the check.
Input from a pipe or `/dev/stdin` cannot be reread, so its stability is
`unknown`.

`check --head WORKTREE` does the same for source, runner configuration and
contract files (`source_stability`). These are before-and-after reads, not
snapshots. An edit that is reverted between the two reads is not seen. Use
commits when you need a repeatable result.

## Plan criteria on the combined tree

With `--plan`, plan rules are evaluated against the combined candidate. A
`test_run` criterion is satisfied only by a run of exactly the reviewed argv,
`cwd`, `setup`, `env` and `junit` on that candidate. Branch-level records and
ad hoc commands do not count. Candidate runs refuse `link` declarations and
JUnit reports that existed before the test started. If the tests modify
tracked source, the run cannot pass.

A passing criterion does not make the whole plan pass. `plan_verification` can
still be `unknown`, for example when contract scope is undeclared.
