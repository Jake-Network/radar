# Intelligent integration verification

Radar separates three questions: which supported properties must pass your gate,
which analysis remains unavailable, and which tests actually ran on the combined
source. A passing selected gate is useful evidence within its stated scope; it
is not comprehensive architectural verification.

## Inspect before execution

```sh
radar check --base main --suggest-tests --json
radar merge-check --base main --branches backend,frontend --suggest-tests --json
```

These commands inspect source and propose test argv without running tests,
loading Python modules, evaluating package scripts or installing dependencies.
The `verification_proposal` includes an inventory, command CWD, priority,
affected files, evidence reasons, runner availability and limitations.

Selection prioritizes exact plan `test_run` declarations, changed tests and
inferred import dependents, Go package companions, then conservative package
fallbacks. Integration-named tests can rank highly when changes span multiple
package roots. Names and import paths are static hints: they neither prove
runtime behavior nor guarantee that all relevant suites were selected.

Selection can also traverse existing explicitly declared producer/consumer
contract edges and schema/field definitions, then continue through consumer
imports to tests. For example, a changed Python model imported by its producer
can reach the producer's declared contract, a TypeScript consumer and that
consumer's tests. `declared_contract_impact` records the supporting manifest
locations while test relevance remains inferred. This requires accepted explicit
bindings recorded as static declarations: discovered, proposed or unknown
contract links are excluded. It is not automatic runtime cross-language mapping,
and unrelated contract identities do not connect merely through similar names.

Conventional files/configuration are recognized for Go, Python unittest/pytest,
Node's test runner, Jest/Vitest and Cargo. Unsupported or ambiguous JavaScript
frameworks remain inventory candidates rather than executable guesses. Plain
Node execution does not provide TypeScript transpilation. Runner availability
checks only an executable; it does not prove installed modules, plugins, service
availability or correct framework configuration.

## Run explicitly selected evidence

After reviewing commands and authorizing execution:

```sh
radar merge-check --base main --branches backend,frontend \
  --verify --suite recommended --allow-execution --timeout 120s --json
# Or select an exact command yourself:
radar merge-check --base main --branches backend,frontend \
  --verify --allow-execution -- python3 -m unittest test_integration
# Explicit subdirectory execution, including an exact subdirectory plan criterion:
radar merge-check --base main --branches backend,frontend \
  --plan .radar/plans/approved.json --verify --allow-execution --cwd services/api \
  -- python3 -m unittest test_api
```

Each command runs against the actual combined candidate in its recorded
repository-relative CWD. Explicit commands default to `.`; `--cwd DIR` selects
a safe subdirectory. A nondefault `--cwd` requires an explicit verification
command and cannot override the per-command CWD of `--suite recommended`. The report separates `selected_tests`, `executions`
and proposal metadata. Observations bind argv, CWD, ordered input commits,
candidate commit/tree, output digest and source integrity. Independent branch
results are never imported as proof of combined success.

The suite is grouped, then bounded by `--max-commands` (default 16) and one
shared timeout (default two minutes, maximum 30 minutes). Commands beyond the
budget are listed as omitted; a required omission blocks the gate rather than
being silently dropped ([TEST_SELECTION.md](TEST_SELECTION.md)). No recommendations or no recognized executed
tests leaves evidence unknown. Missing tools, build/setup failures and timeouts
are execution/environment errors, not test success. Dependency installation is
never implicit. Results can be incomplete if executed code modifies candidate
source; rerun on the intended repaired source instead of reusing that result.

The copied candidate protects the original Git worktree/index from Radar's merge
operations. Executed repository code still has host privileges and may access
host files or networks. **This is not an OS sandbox.** Review untrusted commands
and code before authorization. No command proposal, policy or MCP call can
fabricate execution consent or a human design review. MCP check/merge tools
return bounded summaries by default (ten findings, eight commands, no inventory).
Request `detail: true` only when more analysis is needed; the full response is
still capped at 40,000 bytes. Use CLI JSON for complete local artifacts.

## Require properties without hiding coverage

Create `.radar/integration-policy.json`:

```json
{"version":1,"name":"integration","require":["textual_merge","no_breaking_contracts","integration_execution"],"on_missing":"blocked"}
```

Then append `--policy .radar/integration-policy.json` to the command above.
Inspect `gate.verdict` independently of `status` and `coverage`. Policy verdicts
are `pass`, `fail`, `blocked` or `error`; missing required observations cannot
become passes. Explicit policy exits are 0 for pass, 1 for fail/blocked, 2 for
error. Legacy exits remain compatible without policy. See
[verification policy](VERIFICATION_POLICY.md) for supported check IDs and the
limits of `no_breaking_contracts`.

## Verify reviewed plan criteria

Supply a reviewed versioned plan with exact acceptance commands and meaningful
alternatives. A `test_run` rule can declare repository-relative `cwd` plus
`setup`, `env` and `junit`. Empty CWD means repository root. Exact argv, CWD and
execution configuration must match the reviewed declaration. Every candidate
rule `env` name must be set before execution; missing names are environment
errors. Provenance stores names, never secret values, so this is not proof of
environment-value reproducibility. Legacy ordinary evidence keeps its existing
optional environment pass-through behavior.

```sh
radar merge-check --base BASE --branches A,B --plan .radar/plans/approved.json \
  --verify --suite recommended --allow-execution --policy .radar/plan-policy.json --json
```

A plan policy can additionally require `plan_verification` or an individual
`criterion:CRITERION_ID`. The full plan may remain unknown because contract scope
is undeclared, even when a selected criterion passes. A criterion gate proves
that criterion's bounded property, without hiding unresolved overall scope. Review the policy names against the plan rather than
assuming an undeclared criterion exists. All plan rules are evaluated against
the combined candidate; recognized exact test observations may satisfy the
corresponding declared criteria. Ad hoc commands remain generic observations.

Candidate records additionally bind repository identity, baseline, ordered
input commits, candidate commit/tree, plan/review digests, argv/configuration,
CWD and source digests before/after execution. Missing approval, changed plans,
branch evidence or mismatched commands cannot establish approved candidate
criteria. Source mutation invalidates candidate test records for the suite;
repair and rerun against fresh candidate state.

Candidate plan execution rejects host dependency `link` declarations. Use
explicit reviewed setup inside private state instead, with available local
runtimes/caches; installation/network access is never implicitly authorized.
A pre-existing JUnit report, including one generated by setup before the actual
test, is rejected as stale. Fresh declared JUnit output may support harness
counts, but does not prove test quality or protect against malicious code
fabricating results.

`--evidence-output NEW_PATH` saves a single explicit observation object or an
ordered recommended-suite array according to
[`integration-evidence.schema.json`](../schemas/integration-evidence.schema.json).
The optional `plan_record` carries approved criterion evidence. The original
schema-1 plan/test records remain readable through compatible additive fields.
Raw test output is hashed rather than stored in the integration report.

## Repair and demonstrate

Use finding IDs, failed cases, evidence classifications, source locations and
revalidation instructions as repair inputs. Re-run the same supported invariant
on the repaired combination. Two failed repair attempts are a useful escalation
bound; repeated identical feedback without new evidence is not a reason to loop.

The deterministic demo is:

```sh
go build -o /tmp/radar ./cmd/radar
bash examples/intelligent-verification/demo.sh /tmp/radar
```

It exercises discovered multilingual test selection and combined verification
using Git, Python 3, Node with built-in fetch, Bash, realpath and Linux
`/usr/bin/time`. It opens a loopback HTTP server, so environments that forbid
local sockets cannot run the runtime scenario. It does not require npm downloads
or runtime FastAPI/Pydantic installation: those adapters are static fixtures.
Its actual pass/failure assertions and missing prerequisites are reported by the script. The final milestone
report records executed results; documentation alone is not acceptance evidence. See the
[measured validation](VALIDATION_INTELLIGENT.md) for the generated demonstration,
pinned Click read-only evaluation and stated measurement limits.
