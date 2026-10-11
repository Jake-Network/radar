# External validation observations — 2026-10-11

This evaluation establishes reproducible controlled failures on independent upstream
source trees. It does not establish general reliability, naturally occurring bug
prevalence or a performance advantage. The evaluated development binaries report
`0.1.0-dev`; exact hashes and build metadata identify these observations. Four
public projects are pinned. Hono has an unstable baseline; the full-stack
project's required environment remains unavailable. Existing source
and documentation mutation controls were reused; the two integration challenges
and passing source control were added for this evaluation. These cases are
transparent controlled probes, not an undisclosed blind holdout. Product fixes
address evidence correctness rather than tuning dependency selection to these cases.

See [baseline audit](EXTERNAL_VALIDATION_AUDIT.md),
[runner and reproducibility instructions](../benchmarks/external/README.md) and
the machine-readable artifacts under `benchmarks/external/results/`.

Readable evidence: [Click baseline](../benchmarks/external/results/paired/click-baseline.md),
[Click fixed](../benchmarks/external/results/paired/click-fixed.md),
[chi](../benchmarks/external/results/paired/chi.md),
[Hono](../benchmarks/external/results/hono-fixed.md),
[full-stack](../benchmarks/external/results/baseline/fullstack.md).
[Compact JSON summary](../benchmarks/external/results/summary.json) and
[artifact hashes](../benchmarks/external/results/raw-artifacts.json) accompany them.

## Ground truth and scope

| Project | Immutable upstream revision | Authoritative command | Scope limit |
| --- | --- | --- | --- |
| Pallets Click | `2247b35ea1c47c727d7a06e51fa280e12a863ff6` | Native pytest default suite with fresh JUnit file labels | Upstream default excludes stress and typing jobs |
| go-chi/chi | `67be7d9cafdaeb4e04e887ff78d09e030ee43b00` | `go test -json -count=1 ./...` | Package outcomes do not identify independent test-file outcomes |
| Hono | `73ff6c0e82d66468e28ed439481220f56ab03882` | `npm test` (TypeScript and configured Vitest suites) | Optional runtime/toolchain matrix is not exercised |
| FastAPI full-stack template | `7257e606cb94eada1dca40173e2b7f99dd549bc6` | Declared backend/frontend native commands | Python 3.14, Bun, services and browsers were not provisioned |

Click's large-integer integration challenge has branch A route open-boundary
clamping through a floating-point conversion. Existing tests pass. Branch B adds
an exact-integer boundary test above 2^53, which passes on unchanged code. Their
combined native suite fails that test. Chi branch A removes True-Client-IP support
while existing tests pass; branch B adds a True-Client-IP regression that passes
on unchanged code. Their combined native suite fails. Both are nonconflicting
Git merges, and every individual branch passed its independently executed complete
native suite. Individually failing mutations are counted separately.

Independent revalidation additionally checks tracked source and HEAD before and
after native suites. Paired evidence requires matching image, revision, branch
trees and candidate tree; original Radar timing artifacts remain unchanged.
Untracked additions and temporarily restored source edits are outside this guard.

## Completed Click and chi measurements

Fractions below are case counts from the first run per mode; repeats are timing
samples rather than new defects. "All failed mutations" includes integration
challenges. Independently failing source mutations are a separate subset: Click
and chi each detected 1/1 in every mode.

| Artifact / mode | Integration detection | All failed mutations | False PASS | False FAIL | False BLOCK | File recall | File precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Click baseline, targeted/balanced | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/129 |
| Click baseline, full | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/172 |
| Click fixed, targeted/balanced | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | 2/2 | 2/129 |
| Click fixed, full | 1/1 | 2/2 | 0/2 | 0/2 | 0/2 | 2/2 | 2/172 |
| Chi baseline, targeted/balanced | 1/1 | 2/2 | 0/2 | 0/1 | 1/1 | unavailable | unavailable |
| Chi baseline, full | 1/1 | 2/2 | 0/2 | 0/1 | 0/1 | unavailable | unavailable |

Click native JUnit identifies 43 test files, all present in Radar's inventory;
missing and unsupported files are zero **within that collected suite**. Targeted
and balanced source mutations select all 43 files: no file-count reduction.
Chi inventories 19 files and selects 15 for the integration challenge (21.1% file
reduction), but Go package execution does not provide independent file recall or
precision. Unknown inventory is not converted into zero missing tests.

| Case / mode | Gate first / warm seconds | Ordinary merge + native suite seconds | Separate analysis seconds | Selected execution first / warm seconds | Peak process RSS KiB first / warm |
| --- | --- | --- | --- | --- | --- |
| Click fixed integration, targeted | 54.20 / 67.58 | 12.66 | 4.76 | 11.78 / 16.18 | 141200 / 141248 |
| Click fixed integration, balanced | 64.03 / 56.40 | 12.66 | 6.28 | 15.75 / 13.65 | 141360 / 141196 |
| Click fixed integration, full | 54.33 / 55.88 | 12.66 | 5.06 | 12.54 / 14.06 | 141128 / 141160 |
| Chi integration, targeted | 105.54 / 105.44 | 27.89 | 1.10 | 26.19 / 26.19 | 132348 / 131924 |
| Chi integration, balanced | 107.56 / 107.39 | 27.89 | 1.53 | 26.54 / 26.60 | 132200 / 128480 |
| Chi integration, full | 111.36 / 111.28 | 27.89 | 1.57 | 27.32 / 27.59 | 131772 / 129024 |

The first/warm native baseline measurements were Click 16.91/16.25 seconds
(fixed executable experiment) and chi 52.65/26.19 seconds. The guard-only
revalidation baselines were separate observations: Click 13.91 and chi 62.47
seconds. Both integration gates are slower than their conventional comparison;
these experiments do not demonstrate runtime savings. Attribution confirms base
and singleton selected commands pass, and labels the two-branch combination as
reproducing; it is not proof of causal branch ownership. Missing attribution
rerun durations keep total test execution time unavailable.

Click's passing source control now passes in every mode, and full docs control
passes. Targeted/balanced docs still block. The baseline and fixed binaries also
include concurrent attribution work (exact hashes/build metadata retained), so
elapsed changes are not attributed solely to the cache fix. Loss of source
integrity had prevented meaningful reruns in the original Click observations.

## Hono and full-stack unavailable ground truth

The corrected Hono evaluation completed in 1535.14 seconds. Cache writes no
longer failed and every native suite retained unchanged tracked source/HEAD.
However, its unchanged baseline failed one logger timing assertion: after a
1000 ms sleep it expected `/1s/` but logged `493ms`. The baseline took 126.235
seconds and stopped the `&&` test chain before later configurations. Hono's
logger uses `Date.now`; host clock instability is a plausible explanation,
not a proven diagnosis. This baseline is **not** changed to passing because
later controls pass.

Docs branch/combined and equivalent-source branch/combined native suites passed;
URL-mutation branch/combined suites failed. All three accuracy rows remain
ineligible because the unchanged baseline failed. Hono detection, false PASS,
false FAIL, false BLOCK, file recall and precision are null, not zero. The
full-stack template contributes one additional unavailable row, with dependency
and candidate-symlink limitations. Four projects are represented; two qualify
for controlled accuracy evaluation. This does not establish generalization to
three healthy external environments.

All nine Hono Radar calls returned blocked on matching candidate trees. Candidate
construction omitted checkout-local node_modules, so no recognized selected
execution duration is available. Planning-time runner uncertainty and actual
execution absence remain separate evidence. Targeted source controls select
84/118 inventoried files (28.8% reduction), while balanced/full select118/118;
missing inventory, recall and precision are unavailable without an independent
file inventory. These selection counts do not show runtime savings.

| Hono URL mutation / mode | Blocked gate seconds | Native merged suite + merge seconds | Separate analysis seconds | Peak process RSS KiB |
| --- | --- | --- | --- | --- |
| targeted | 30.62 | 105.22 | 38.48 | 146748 |
| balanced | 27.30 | 105.22 | 34.06 | 112024 |
| full | 28.17 | 105.22 | 32.27 | 102672 |

The earlier Hono attempt and deliberately superseded oracle-only attempt are
retained separately. No Hono warm repeat or successful cold baseline is claimed.
`results/summary.json` is the compact machine-readable report; lossless raw JSON,
input hashes and per-case timings remain alongside it.

## Confirmed correctness fixes

1. **Generated pytest cache invalidated clean source evidence.** Click's passing
   source control was blocked despite an independently passing native suite.
   Exact `.pytest_cache` directory components now use the existing generated-file
   exclusion shared by indexing/evidence. Tracked cache changes, sibling names
   and newly created source files still invalidate evidence.
2. **Successful candidate execution retained a planning-time runner blocker.**
   Recognized successful Jest/Vitest execution against unchanged source now
   resolves only its matching runner uncertainty. Missing runners, zero observed
   cases, other CWD commands, omissions and inventory gaps stay incomplete.
   Permanent tests use trusted stand-in recognizer output; this is not a claim
   that Hono candidate dependency provisioning is supported.
3. **Workspace `last` could replay an older record.** Required repository checks
   reproduced stale selection with backward/repeated clocks. Atomic timestamp
   reservations and a publication recheck preserve logical allocation order
   without changing run-record format. Frozen-clock, concurrent publication and
   delayed-allocation regressions cover this fix.

The shared benchmark oracle also stopped accepting pytest summaries with zero
observed cases and distinguishes anchored missing-module diagnostics from
source-import failures. Pairing rejects missing/modified source evidence and
output aliases that would overwrite input artifacts. No accuracy denominator
includes unavailable or zero-test ground truth.

## Remaining limits and engineering priorities

- Documentation-only targeted/balanced runs select zero commands and remain
  blocked by the observed-execution requirement. Report this separately from
  source failure; a future explicit no-execution policy needs its own semantics.
- Central Python import hubs select most or all inventoried tests. Conservative
  file relationships should not be weakened without independent recall evidence.
- The benchmark Go oracle does not qualify unparsed compiler/build failures;
  those remain unavailable, and compiler-error mutation accuracy is not measured.
  Product source-vs-toolchain classification remains covered by existing tests.
- Dynamic imports, computed runtime filenames, indirect schema loaders and
  unsupported TypeScript resolution cannot be declared safe from absent edges.
  They need new independent challenges; no general coverage claim is made here.
- Candidate construction does not transplant untracked node_modules. Dependency
  availability in an upstream checkout does not prove candidate runner availability.
- Full-stack evaluation needs a separately provisioned offline service/browser
  image. Its missing dependencies and rejected repository symlink are explicit
  unavailable scenarios rather than passing or failing ground truth.
- Stabilize/monitor host time before qualifying Hono; characterize repeated
  baseline failures without cherry-picking a passing run.
- Prioritize native-suite inventory comparison and explicit candidate dependency
  provisioning before selection optimizations. Add independent dynamic/schema
  challenges, then measure recall on a larger holdout rather than tune these probes.

## Measurement boundaries

Each external test ran in a disposable, non-root, offline Docker container with
read-only inputs/root, dropped capabilities, no host credentials and CPU/memory/
process limits. Dependency image preparation was separate. Container images and
binary/input digests are recorded. Rebuilds are not claimed bit-identical because
Debian repositories remain mutable.

Cold measurements mean first baseline invocation in a new container; host page
cache was not flushed. Radar gates followed oracle execution and have no cold
gate measurement. Warm repeats reuse tool caches with fresh candidates. Concurrent
benchmark/development workloads limit timing comparability. Process maximum RSS
is observed; aggregate container peak memory is unavailable. Selection reduction
is file-count reduction, not measured runtime savings. Static analysis is a
separate check probe. The `unavailable_runner_test_files` field records the static proposal probe;
actual runner outcomes are in execution evidence and must take precedence.
Selected-command durations exclude attribution reruns when
those durations are not exposed, so total execution and residual phase time
remain unavailable for those cases.

## Verification

The committed source at `e340bbfc57e00c3e641225683c11a36b57107363`
(tree `44302bb9ad3beddd8ce28598d59057213b441632`) was extracted from a Git
archive into `/tmp/radar-validation-source-final`; every extracted regular file
was compared to its archive bytes. `verification-source.json` records provenance.
All verification targets Linux. The Windows-mounted checkout is only a WSL
filesystem location, not a Windows platform validation. Go parents temporarily
waited in WSL `p9_client_rpc` after test
children exited. Final mounted-path checks subsequently completed with exit0:
plain CLI288.830 s/composition47.218 s; race CLI303.057 s/composition48.520 s.
The independently verified Linux mirror also passed:

- `go test ./...` (CLI 132.143 s; integration 43.814 s).
- `go test -race ./...` (CLI 150.512 s; integration 46.156 s).
- `go vet ./...` and native `go build`.
- Existing benchmark regressions: 14; external harness/report regressions: 31.
- GitHub Actions integration regressions: 8; workflow regressions: 5; release
  regression checks: 2.
- Integration smoke and selection-eval demo assertions, plus `git diff --check`.

The mirror contains all code/tests; remaining working-tree changes at snapshot
time were this report, benchmark documentation and result artifacts only.
Hosted CI and a provisioned full-stack runtime remain unverified by these local
Linux checks.
