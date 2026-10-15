# Verification reference

Radar separates policy verdicts, analysis coverage and observed execution.
For commands and examples, see [Usage](USAGE.md); for agent integration, see
[Agent guide](AGENT_GUIDE.md).

## Verdicts and coverage

- `gate.verdict` answers whether required checks passed: `pass`, `fail`,
  `blocked` or `error`.
- `coverage` records what analysis covered, skipped or could not support.
- `status` is the aggregate analysis result; `checks` retain individual results
  and evidence categories: `verified_static`, `verified_tool`, `observed_test`,
  `inferred`, `proposed`, `unknown`.

A passing gate does not imply complete coverage or behavioral correctness.

| Gate verdict | Exit | Meaning |
| --- | --- | --- |
| `pass`, static | 0 | Required static checks passed; tests did not run. |
| `pass` with `--run` | 0 | Required checks passed on the committed combined candidate. |
| `blocked` | 1 with policy/`--run`; legacy static default 0 | Required evidence is missing, incomplete or unsupported. Terminal: `NOT VERIFIED`. Always inspect JSON `gate.verdict`. |
| `fail` | 1 | A required check failed, or `on_missing: fail` converted missing evidence to failure. |
| `error` | 2 | Invocation, analysis or execution error, including timeout. |

Default `gate` requires `textual_merge` and `no_breaking_contracts`, plus
`migration_order` when the branches change a recognized migration history
([capabilities](CAPABILITIES.md#migration-order)). Relevant supported symbol,
direct lockfile and unique-key checks are also required when applicable. `--run` adds
`integration_execution` and, when a suite is selected, `test_selection`; declared
regeneration also requires `generated_drift`.
Uncovered changes, incomplete inventory and omitted required commands cannot
be compensated for by a passing test. Inspect the gaps or try `--suite full`.
Without execution or a policy requiring it, missing test evidence alone does
not fail a static gate.

Documentation-only changes select no tests: `--run` has no execution evidence
and remains `NOT VERIFIED`. A test may read a document at runtime, which static
analysis cannot rule out. A policy can deliberately omit execution requirements.
When no contracts or base obligations exist, `no_breaking_contracts` passes
trivially; human output shows `· Contracts none declared`, not a check mark.

## Policies

Policies name required checks; they never authorize code execution.

```json
{"version":1,"name":"integration","require":["textual_merge","no_breaking_contracts","integration_execution","test_selection"],"on_missing":"blocked"}
```

```sh
radar gate --run --policy .radar/integration-policy.json feature/api feature/web
radar check --base main --policy .radar/analysis-policy.json
```

| Check ID | Pass condition |
| --- | --- |
| `textual_merge` | Branches merge without conflicts. |
| `no_breaking_contracts` | No supported breaking change against declared or base contracts. |
| `migration_order` | No migration history conflict new to the combination or a single branch. |
| `declared_contracts` | Accepted manifest; every binding analyzed. |
| `discovered_contracts` | No supported break among discovered candidates. |
| `dependency_impact` | Reverse import analysis completed. |
| `source_stability` | Working inputs unchanged during analysis. |
| `configuration_stability` | Selected plan and policy files unchanged during the run. |
| `integration_execution` | Every required command ran and passed on the candidate. |
| `test_selection` | No uncovered changed file or omitted required selection. |
| `plan_verification` | Whole plan verified on the candidate. |
| `criterion:ID` | Named plan criterion verified on the candidate. |
| `test:SELECTION_ID` | Named selected test command passed. |

A `--policy` replaces the default list, including the conditional
`migration_order`: omit it and migration conflicts stay visible without
failing; require it and a change without migrations is blocked.

Unknown IDs count as missing evidence. `on_missing` defaults to `blocked`;
`fail` is also supported. Warnings, `unknown` and `incomplete` never satisfy
requirements. Unrequired problems remain visible without changing the verdict.
A policy omitting `test_selection` can pass with gaps; text labels that pass
and JSON preserves the gaps.

With `--policy`, commands use gate exit codes. Without it, `check` and
`merge-check` preserve their existing exit behavior. `--require-complete` is
the older all-or-nothing option and cannot be combined with `--policy`.

## Contract obligations

The base revision's `.radar/contracts.json` defines obligations. Head changes
cannot erase them by deleting the manifest, narrowing/removing a binding,
clearing its consumer or moving its schema. Both base and head declarations
are checked. Discovery proposals do not automatically become bindings.

| Head change | Result |
| --- | --- |
| Binding retained, supported breaking schema change | Fail (`contract_<kind>`). |
| Obligation removed/narrowed/moved, consumer retained, breaking change | Fail (`contract_obligation_removed`). |
| Obligation removed, consumer retained, no schema change | Blocked. |
| Obligation and consumer file removed together | Pass, recorded as `consumer_removed`. |
| Obligation retired with a reason | Pass with `contract_obligation_retired` warning. |
| Unparseable manifest or retirement without reason | Blocked in `check`; error in `merge-check`. |
| Unsupported schema/pointer or missing consumer for a retained obligation | Blocked. |

Retire an obligation explicitly in the same change:

```json
{"version":1,"bindings":[],"retired":[{"id":"order-summary","fields":["currency"],"reason":"storefront now reads currency_code"}]}
```

Omit `fields` to retire the entire binding. An approved plan with a
`contract_deltas` entry `{"operation":"remove","contract":ID}` also retires
it. Retirement already present in the base does not apply again. Local review
declarations do not authenticate reviewers; protect `.radar/` through repository
review controls when necessary.

## Inputs and evidence

`gate` builds a private combined candidate from commits. Dirty files are
excluded and reported under `worktrees`; commit desired inputs and rerun.
Detached worktree commits must be named by SHA. Candidate building does not
write source refs, index or working trees, and never fetches missing history.

`watch` also uses commits by default. `--include-dirty` opts into private
previews with staged, unstaged and untracked provenance. Preview identity is
distinct from committed evidence. Incomplete, unsupported or inconsistent
snapshots cannot pass for the live state. Input changes invalidate the displayed
verdict; superseded results are discarded. Polling may miss edits reverted
between observations, and unsaved buffers are outside scope. See [Usage](USAGE.md).

Plan and policy files are read once, SHA-256 recorded in `configuration_inputs`,
and reread afterward. Changes make `configuration_stability` incomplete and
invalidate a dependent pass even if the policy omits that check. Pipes and
`/dev/stdin` cannot be reread, so stability is `unknown`. `check --head WORKTREE`
similarly checks source, runner configuration and contracts before and after;
reverted intermediate edits may be missed. Commits provide repeatable inputs.

## Evidence

Plan reviews bind the baseline and plan digest; changes invalidate review.
Evidence binds repository identity, exact commit/candidate tree, plan digest,
criterion, argv and execution directory. Stale, altered or other-candidate
records are rejected. Branch-level records never verify a combined candidate.
Persisted records are insert-only; hashes detect alteration, not author identity.

With `--plan`, rules evaluate on the combined candidate. A `test_run` criterion
requires exactly the reviewed argv, `cwd`, `setup`, `env` and `junit` configuration.
Candidate execution requires a valid reviewed plan; ad hoc commands do not
satisfy its criteria. A passing criterion need not verify the whole plan:
undeclared contract scope, for example, can leave `plan_verification` unknown.

`cwd`, dependency paths and JUnit paths must stay inside the repository.
`setup` is a list of exact argv arrays; `env` lists variable names, never values,
and those variables must exist. Candidate plan runs reject dependency `link`
declarations. A JUnit report must be fresh: neither pre-existing nor generated
by setup before the test runs. Declared reports supplement recognized output;
missing reports cannot turn observed failures into passes. Source changes
during execution invalidate passing evidence.

## Test selection

Selection establishes a static relationship, not behavioral coverage. Inventory
reads configuration as data, without evaluating scripts, importing Python or
running collection hooks. Limits: 10,000 files, 1 MiB per file, 32 MiB total;
skipped input remains visible.
Oversized files that cannot match supported test path conventions leave a
warning without making inventory incomplete. Oversized tests and configuration
still block selection. Rust source remains potentially test-bearing because
tests can be inline. This exception does not establish import relationships for
skipped helpers: indexing has a separate 2 MiB source limit, and missing import
relationships remain unknown.

| Mode | Selection |
| --- | --- |
| `targeted` | Plan commands, changed tests, import/declared-contract relationships, named data/schema references, Go/Java package companions and integration-named suites for multi-package changes. |
| `balanced` (default; alias `recommended`) | Targeted selection plus package-root fallbacks for changes without direct relationships. |
| `full` | Plan commands and whole-suite command per framework/package root. |

Preview: `radar check --base main --suite targeted`. Direct relationships are
`required`; package fallbacks are `optional`. Documentation-only changes select
nothing. Only explicit bindings extend selection through producer → consumer
→ tests (`declared_contract_impact`); discovered candidates do not.

| Framework | Native execution/selection |
| --- | --- |
| Go | Grouped package-level `go test -json`; no per-test filtering. |
| Python unittest / pytest | unittest discovery; pytest paths. |
| Node test / Jest / Vitest | File paths; unknown frameworks or TypeScript without recognized Node loader are unsupported. |
| Cargo | Per-crate `cargo test --manifest-path`; no per-test filtering. |
| Maven / Gradle | Surefire `-Dtest` / Gradle `--tests`, per class/module. |
| C/C++ with CMake/CTest | Whole registered project suite; no CTest `-R`. |

Jest related-test selectors, Vitest `related`, Nx affected and pytest-testmon
are not used: they require repository execution, services or prior coverage.
Read-only commands and MCP never run selectors, collection hooks or scripts.

### Java and C/C++ restrictions

Java tests require test annotations or `TestCase` outside comments. Only the
module's `src/test/java` source set is executable; conventional test names
elsewhere remain unsupported. The nearest Maven/Gradle build selects the tool;
both or neither is unsupported. Maven reactor modules and Gradle projects must
be declared literally. Dynamic/relocated Gradle settings are unsupported.
Java same-package companions are inferred relationships, not proven usage.

Committed wrappers run only with the pinned distribution cached and JVM on
`PATH`. Cache validation requires the URL-specific entry and launcher, Gradle's
completion marker, or Maven's supported JAR/only-script layout. JAR wrappers
also need their committed JAR and retained archive. Custom caches, forced
downloads/unpacking, escaped URLs and unknown variants remain unavailable.
Only default cache paths and plain ASCII HTTP(S) ZIP URLs are supported.
Each Java command uses a fresh candidate snapshot; only default Surefire/Gradle
report locations are accepted. Without recognized reports it cannot pass.
Full mode runs the module test phase/task, subject to the tool's include rules.

C/C++ test names/directories need a test macro or `main` outside comments.
The outermost enclosing CMake project must enable testing. Radar configures,
builds and runs its entire CTest suite in `build/radar-ctest`, using Unix
Makefiles, FetchContent disconnected and JUnit output. It requires `ctest`
(3.21+), `cmake` and `make`. Make/Meson/Bazel-only tests are unsupported;
declare reviewed `test_run` setup and a report instead:

```json
{"kind":"test_run","command":["ctest","--test-dir","build","--output-junit","ctest.xml"],"setup":[["cmake","-S",".","-B","build"],["cmake","--build","build"]],"junit":"build/ctest.xml"}
```

FetchContent disconnection does not stop `ExternalProject` or `file(DOWNLOAD)`.
The private candidate is not an OS sandbox.

## Execution

Code runs only with explicit authorization (`gate --run` or
`merge-check --verify --allow-execution`). Matching per-file commands group by
framework, directory, tier and runner, up to 200 paths; `grouped_from` preserves
origins. Plan, unittest discovery and Cargo commands are never grouped.
Grouped tests share process state, which may differ from per-file execution.

- `--max-commands` defaults to 16 after grouping; required commands go first.
  Omitted commands are `budget_exceeded`; required omissions also block.
- `--timeout` covers authorized setup, generation and selected execution: gate defaults to 10 minutes, merge-check
  to 2 minutes, maximum 30 minutes. Unstarted commands are `time_budget_exhausted`.
- Commands are sequential; Java commands additionally use fresh snapshots.
  Skipped/unavailable required commands make execution incomplete even if other
  commands pass. Confirmed source failures take precedence over skips.

Radar never automatically installs dependencies. Untracked `node_modules` and
`.venv` are not copied from the user's checkout; unavailable runners block
verification. Explicit `.radar/runners.json` setup may install only after execution
opt-in and safely reuse declared artifacts; a source-changing setup cannot publish
cache entries. Prepare caches or declare reviewed setup. Single-revision plan execution can declare dependency
links; combined candidate plan execution rejects them.
Regular verification and legacy plan setup use offline Go/Python/Cargo/npm
settings and Maven/Gradle offline flags. Explicit profile dependency setup may
download from registries after execution opt-in; inherited secrets/startup settings
remain scrubbed. Verification runs continue
while reusing available local caches and supported tool homes. Maven reads the
account's `~/.m2`; Gradle user-home properties remain visible to builds.

| Observation | Result |
| --- | --- |
| Recognized executed tests, none failed | `passed`. Exit 0 alone is insufficient. |
| Recognized failing tests or located repository-source compile/link errors | `failed`; build setup errors of this kind fail in phase `setup`. |
| Python `ImportError` for a removed name | `failed`. |
| Missing modules/toolchains/headers/libraries, checksum/registry errors, unresolved JVM dependencies, CMake configuration errors | Environment `error`. |
| Compiler location outside candidate, generated/dependency source or nonexistent file | Environment `error`. |
| Missing runner/command, timeout or output limit | `error`. |
| Exit 0 without recognized tests | `unknown`. |

Dependency/toolchain diagnostics prevent source attribution, even when mixed
with apparent compile errors. Source locations must be repository-relative and
validated inside the candidate. Missing headers receive `module_missing`, which
may mean either a system dependency or a branch-renamed header.

Optional `--attribution-runs N` diagnoses source failures on base/subset candidates
under execution authorization; default is 0. `isolated` means all smaller
combinations passed, `base` means the original failure also exists on base, and
`inconclusive` means insufficient/comparably different evidence. Gate diagnostics
share its timeout; watch uses a separate diagnostic budget after Fast Gate.
Each subset runs once; flakiness is possible. Attribution never changes the gate,
selection or candidate evidence and is a repair lead, not causality proof.
