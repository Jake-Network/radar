# Gate verdict and analysis coverage

`gate`, `check` and `merge-check` report three separate values:

- `gate.verdict`: `pass`, `fail`, `blocked` (required evidence absent/unestablished), or `error` (execution/environment error, including timeout).
- `coverage`: analyzer scope, exclusions, unavailable capabilities and uncertainty.
- Existing `status` and individual `checks`: preserved legacy whole-analysis statuses and evidence categories.

A passing selected gate does not imply complete architectural or runtime correctness.
Read-only check reports unexecuted tests as a coverage gap; it does not execute them.

A policy is a small versioned JSON object, not a command or authorization language:

```json
{"version":1,"name":"integration","require":["textual_merge","no_breaking_contracts","integration_execution"],"on_missing":"blocked"}
```

```sh
radar check --base main --policy .radar/analysis-policy.json
radar merge-check --base main --branches backend,frontend --policy .radar/policy.json
radar merge-check --base main --branches backend,frontend --policy .radar/policy.json \
  --verify --allow-execution -- python3 -m unittest test_integration
```

For read-only analysis, require `dependency_impact`, `source_stability`, and
`no_breaking_contracts`. Requiring `declared_contracts` additionally requires
an analyzable accepted manifest; absent bindings cannot satisfy that requirement.
Other check IDs include `discovered_contracts`, `plan_verification`,
`integration_tests` (read-only missing observation), `integration_execution`
and `textual_merge`. Unknown required IDs are missing evidence, never passes.
The absence of a supported authoritative contract finding does not establish
all inferred consumers or unsupported schema constructs as compatible. An absent
manifest permits this bounded check; malformed configured contracts or missing
declared schema inputs prevent its pass. Candidate policies can also require
`criterion:<id>` or `test:<selection-id>` for particular observations.

`on_missing` accepts `blocked` (default) or `fail`. Warnings, unknown and
incomplete results cannot satisfy a required passed check. Unrelated analyzer
limitations remain visible without vetoing an explicit policy.

With explicit `--policy`, exits are 0 for pass, 1 for fail/blocked and 2 for
error. Without policy, existing exit behavior and aggregate `status` are kept.
`--require-complete` retains its legacy whole-analysis interpretation and prints
migration guidance; it is not silently redefined. It cannot be combined with
`--policy`. Use explicit required checks for practical CI gates.

## Contract obligations cannot disappear silently

The **base** `.radar/contracts.json` defines the contract obligations under
review. A head (or combined candidate) may add obligations, but it cannot
remove them by deleting the manifest, emptying `bindings`, dropping a binding,
narrowing a binding's consumed `fields`, clearing its `consumer` reference or
moving it to another schema/pointer. Radar evaluates every incompatible schema
change against the base declaration as well as the head declaration:

| Head state of a base binding | Schema change used by the base declaration | `no_breaking_contracts` |
| --- | --- | --- |
| unchanged | incompatible | `fail` (`contract_<kind>`) |
| removed, narrowed or moved, consumer file still present | incompatible | `fail` (`contract_obligation_removed`) |
| removed, consumer file still present | none | `blocked` (unverified obligation) |
| removed together with its consumer file | any | passes; listed as `consumer_removed` |
| explicitly retired (see below) | incompatible | passes with a `contract_obligation_retired` warning |
| head manifest unparsable, retirement without reason | any | `blocked` (check) / `error` (merge-check) |
| moved to an unanalyzable schema or pointer, unsupported construct in a used field, declared consumer file missing | any | `blocked` |

`gate.NoBreaking` now receives the analysis's `unverified_obligations` in
addition to findings: the absence of error findings alone never passes.
Repositories that never had a manifest are unaffected and keep manifest-free
analysis.

Retire an obligation with a reviewable manifest entry introduced by the change
(a retirement already present at base never applies again):

```json
{"version": 1, "bindings": [],
 "retired": [{"id": "order-summary", "fields": ["currency"],
              "reason": "storefront reads currency_code from release 4.2"}]}
```

Omit `fields` to retire the whole binding. A digest-bound approved plan whose
`contract_deltas` contain `{"operation": "remove", "contract": ID}` retires
that binding too. Radar records but cannot verify who reviewed a retirement;
protect `.radar/` with code ownership if that matters. Every change to an
obligation is reported in `declared_contracts.obligation_changes`
(`check`) or `contract_obligations` (`merge-check`).

The schema change is additive: `retired`, `obligation_changes` and
`unverified_obligations` are new optional fields, and older manifests load
unchanged.

## Mixed-source and omitted-test observations

## Gate defaults and terminal results (0.4)

`radar gate --run` defaults to balanced selection and requires `textual_merge`,
`no_breaking_contracts`, `integration_execution` **and `test_selection`**.
A passing command cannot compensate for uncovered changes, incomplete inventory,
or omitted required verification. This deliberately blocks some runtime-read or
dynamic-import changes whose tests cannot be related statically. Inspect the
reported paths; a reviewed `--suite full` runs all supported inventoried suites
and checks inventory omissions, but does not establish behavioral coverage or
discover every test in the repository.

Explicit policies keep their named requirements. A limited policy may pass with
uncovered changes; terminal output identifies that limited result and JSON keeps
all check/coverage gaps. Policies do not grant execution permission.

| Verdict | Exit | Meaning |
| --- | --- | --- |
| `pass` without `--run` | 0 | Selected static requirements passed; combined tests have not run |
| `pass` with `--run` | 0 | Named policy requirements passed on committed candidate; bounded evidence only |
| `blocked` | 1 | Required evidence missing, incomplete, unsupported or runner unavailable before execution |
| `fail` | 1 | Required known violation/test failure; or `on_missing: fail` maps missing evidence to failure |
| `error` | 2 | Invocation, analysis or observed execution error/timeout |

Individual checks preserve simultaneous failures and missing evidence. Discovered
test files, selected commands/files, executed commands and recognized harness
test counts are distinct. Import relationships are inferred relevance, never
behavioral coverage. A runner absent before execution is a blocked omission;
a runner that starts and encounters an execution error is an error observation.

Gate combines **committed revisions only**. `worktrees` reports each registered
worktree's staged, unstaged and nonignored untracked paths, detached state and
whether its tip equals an input/base commit. All uncommitted content is excluded;
commit intended edits and rerun. Detached worktree tips are not auto-selected;
name the SHA explicitly. Status inspection is a read-only boundary observation,
not a filesystem lock, and does not prove the absence of transient writes.
Bare entries are skipped. Status inspections run with at most four concurrent
Git processes and retain listing order. Global ignore files, including
conditional configuration includes, are read as data without enabling global
fsmonitor or hooks. If worktree listing is unavailable (including Git before
2.36 without porcelain `-z`), `worktree_inspection_error` and a terminal warning
report the missing observation; the committed candidate verdict is preserved.

Gate options can occur before or after branch arguments. `--` terminates options.
An exact branch name containing commas takes precedence over the historical
comma-separated reference shorthand. Other command parsers are unchanged.

`check` pins named committed revisions before analysis. WORKTREE boundary
fingerprints include source, runner configuration and accepted declarations,
and are checked after test recommendations. Changed/unreadable/budget-limited
observations invalidate passing checks even if the selected policy omits
`source_stability`. Transient writes reverted between boundaries are not detected;
use immutable checkpoints for authoritative repeatability.

Full selection accounts for every inventoried test with executable commands or
explicit blocking omissions. Targeted/balanced recommendations also preserve
required unsupported candidates and pre-planning limit omissions. Optional
fallback exclusion keeps its documented meaning. A confirmed failure remains
failed if later observations encounter coverage/environment errors or timeouts;
the additional gap stays visible in per-command metadata/checks.
