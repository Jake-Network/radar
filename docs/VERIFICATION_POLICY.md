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
