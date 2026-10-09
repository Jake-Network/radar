# Gate verdict and analysis coverage

`check` and `merge-check` now report three separate values:

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
