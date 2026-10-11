# Radar external-validation baseline audit

Audit describes the checkout at the start of external validation, before this task's correctness fixes, including pre-existing uncommitted attribution work. No implementation changes made by the auditor. Historical numbers below are documentation observations, not rerun evidence.

## Reusable infrastructure

- `benchmarks/evaluate.py`: manifest version 1; immutable external SHA validation; local checkout acquisition only; private tree reconstruction/mutations; individual branch full-suite observations; combined candidate independent full-suite observations; static and runtime Radar verdicts; bounded subprocess output/timeout; GNU-time RSS when available; JSON + Markdown. External execution requires explicit wrapper and operator attestation. Extend this, not a second runner.
- `benchmarks/manifests/{click,hono,chi,fastapi,trpc,axum}.json` already pin full external SHAs with failing/docs controls. `integration.json` and `monorepo.json` are trusted development fixtures, unsuitable as independent holdouts.
- `benchmarks/test_evaluate.py`: permanent harness tests. `examples/selection-eval/evaluate.py` independently runs mutation suites and failing-file labels, but is older selection-only infrastructure and should supply reusable labeling concepts rather than another gate runner.
- Existing historical external reports are static-only (no independently observed external full-suite ground truth). Click prior selection evaluation was executed, but central imports selected all 43 files. Those results cannot establish generalization.

## Supported scope and assumptions

- `internal/integration/candidate.go`: exact committed base/input SHAs, ordered Git merges into private repository, original refs/index/worktree untouched, deterministic candidate dates. Symlinks, submodules and unsafe paths are rejected. Shallow boundary copied; missing history is reported, never fetched.
- `internal/git/private.go`, `git.go`: argv subprocesses, time/output bounds, no hooks/fsmonitor/global configuration/lazy fetch/replacement objects. Private repository is NOT an OS sandbox.
- `internal/integration/analyze.go`, `gate.go`: textual merge and declared contract compatibility are default static requirements. With suite execution, default requirements additionally include observed execution and test selection. `gate.verdict`, aggregate status, individual checks and coverage remain separate.
- `internal/contracts/schema.go`, `analysis.go`, `direction.go`: explicit bindings only; JSON/YAML documents, JSON pointers, bounded JSON Schema/OpenAPI subset, local refs and direction-dependent compatibility. External/recursive refs and unsupported keywords are opaque; absent declarations are trivially no-break rather than completeness. Base obligations survive manifest deletion/narrowing unless consumer deletion or explicit reasoned retirement applies. Static discovered candidates never become authoritative bindings.
- `internal/testselection/selection.go`: conventional file/configuration inventory, 10k relevant files / 1 MiB per input / 32 MiB total; skipped inputs have diagnostics. Go, pytest/unittest, Node, Jest/Vitest, Cargo, Maven/Gradle and CMake/CTest are recognized to documented degrees.
- `internal/testselection/impact.go`: reverse imports and verified explicit contract edges; bounded graph traversal. `targeted` follows direct relationships, changed tests, Go/Java package companions and named cross-root integration tests. `balanced` adds optional package fallbacks. `full` executes recognized inventoried framework/root commands, not an independent guarantee of upstream test coverage.
- `internal/indexer/tsresolve.go`, `resolve.go`: current TS resolver already supports tsconfig aliases/baseUrl, relative extends, repository package names/exports and .js ESM names pointing to .ts. Package-based extends, arbitrary build/runtime resolution and dynamic behavior remain unsupported. Python resolves project/src layouts and package __init__ imports; conservative file granularity makes central re-export hubs reach most tests.
- Full pytest commands explicitly enumerate inventoried files (`testselection/plan.go`), unlike upstream's native collection. Missing unconventional files or framework discovery differences must be measured against independent collection, not treated as complete inventory.
- Grouped execution shares process state; package-level Go/Cargo/CTest commands execute more cases than selected file counts imply. File reduction is not runtime savings.
- `internal/integration/execute.go`: required omissions/unavailable runners/budget gaps block; output exit 0 with no recognized tests stays unknown; timeout/environment error are distinct from source test/compiler failure. Tests can alter sources, causing evidence invalidation. Dependency installs are never automatic; untracked node_modules/venv do not survive candidate construction. Offline environment settings are not network containment.
- Current uncommitted attribution code reruns base/singletons/subsets and retains inferred leads on incomplete evidence. These selected-command reruns cannot establish complete-suite branch passing; external integration-only labels must use the independent oracle on both branches and combined candidate.

## Confirmed baseline failure mode

Documentation-only targeted/balanced changes select zero commands (`testselection/selection.go:documentation`, `plan.go:Plan`). Selection itself is complete, but `integration/execute.go:runVerification` initializes zero selected executions to `unknown`; default execution requirement therefore blocks. Existing `docs/BENCHMARKS.md` records the same observed false block. This is a mismatch between a deliberate no-tests-needed static decision and required observed test evidence; any fix must state no tests executed and must not grant general test PASS.

## Evaluation risks requiring explicit measurement

- Strongly connected import hubs overselect; should remain conservative until independent failing-test data justifies symbol-level alternatives.
- Literal runtime data filenames get lexical relationships; dynamically constructed paths, indirect schema loaders, dynamic imports and aliases outside supported config may be uncovered or entirely missed. A relationship is inferred, not behavioral coverage.
- Jest/Vitest proposals force `ToolAvailable=false`, yielding Plan blocking before execution. Actual private-runner availability is separately checked by `runVerification.unavailable`; stale static blocking may remain if dependencies become available in the candidate. Reproduction is required before labeling this a bug.
- Existing harness integration eligibility requires individually passed complete suites plus combined failure, but baseline viability should be required explicitly, failures must be recognizable independently, and unavailable/timeout observations must not enter defect denominators.
- Existing selection precision counts selected files observed failing, a lower bound on usefulness; neither all selected passing files nor transitive dependency relationships imply irrelevant selection.
- Acquisition/installation must occur separately inside protected disposable infrastructure. An operator-supplied wrapper/attestation alone cannot prove its containment. No external runtime code may be executed on this host.

## Baseline reporting boundaries

Report SHA/tree/manifest and executable digests; full-suite definition and collection inventory; every individual and combined oracle observation; Radar static, selection and runtime verdicts separately; missing inventory, unsupported and unavailable rows; cold/warm cache conditions; output recognizer limits; wall/RSS measurement availability. Baseline and mutation suite execution are experimental setup costs and should be separate from a user's single Radar gate latency. Integration-only detection must never include individually failing mutations.
