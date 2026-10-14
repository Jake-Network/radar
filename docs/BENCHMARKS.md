# Benchmarks and external validation

These small, controlled evaluations measure selection and combined-candidate behavior. They do not
establish general reliability, naturally occurring defect rates, accuracy on unseen codebases,
throughput or a performance advantage. External observations below were recorded on **2026-10-11**,
using development binaries reporting `0.1.0-dev`; artifact hashes identify the actual executables.

## Development fixtures and historical selection

`examples/selection-eval/evaluate.py` applies known defects to a passing commit, runs the complete
suite for failing-file labels, then queries read-only selection.

```sh
go build -o /tmp/radar ./cmd/radar
python3 examples/selection-eval/evaluate.py fixture --radar /tmp/radar --out /tmp/eval
RADAR_BENCHMARK_BIN=/tmp/radar python3 -m unittest discover -s benchmarks -p 'test_*.py'
python3 benchmarks/evaluate.py benchmarks/manifests/monorepo.json \
  --radar /tmp/radar --allow-execution --out /tmp/radar-monorepo
```

The Python/JavaScript/JSON Schema/Go fixture has 13 test files and 12 mutations:

| Metric | Targeted | Balanced |
| --- | --- | --- |
| Failing mutations detected | 11/11 | 11/11 |
| Failing-file recall | 16/16 | 16/16 |
| Precision | 0.59 | 0.33 |
| Test files skipped | 83% | 69% |

Initial targeted recall was 0.56, exposing Python-root, schema-path and docs-only fallback bugs.
This fixture was used to fix Radar; it is not an independent holdout. The gate evaluator caught
11/11 failing mutations and 16/16 failing-file groups, executing 48/156 file observations. Docs-only
changes passed the full suite but blocked the default execution gate because no tests were selected.
Raw results: `examples/selection-eval/results/` and `benchmarks/results/`.

Historical Click selection at `2247b35e` used eight predefined mutations and selected all seven
failing files. Every `src/click` change selected all 43 test files through its central import hub:
about 4 s selection versus 6 s full-suite execution, with no test-time savings.

Historical static probes below ran no external tests and describe one source change per project;
they are distinct from the runtime evaluation below.

| Project | Selected/discovered test files |
| --- | --- |
| FastAPI | 485/485 |
| Hono | 118/118 |
| tRPC | 23/183 |
| go-chi/chi | 16/19 |
| tokio-rs/axum | 24/168 (gate rejects repository symlink) |
| Click | 43/43 |

Docs-only probes selected zero files in all projects. Warm-cache Linux indexing plus SQLite write
measured about 0.37 s for Radar (109 files) and 0.2 s for a synthetic 1,000-file TypeScript chain.
No incremental indexing or large-monorepo measurement is claimed.

## Independent upstream evaluation

The [Docker runner](../benchmarks/external/README.md) extends the manifest evaluator, using
independently executed baseline, branch and merged native suites. It never automatically clones or
installs dependencies. External tests require explicitly authorized, disposable isolation; a private
Git candidate alone is not a sandbox. CI runs offline harness regressions, not upstream code.

| Project | Pinned revision | Native ground truth and scope |
| --- | --- | --- |
| Click | `2247b35ea1c47c727d7a06e51fa280e12a863ff6` | Default pytest + fresh JUnit labels; excludes stress/typing jobs |
| chi | `67be7d9cafdaeb4e04e887ff78d09e030ee43b00` | `go test -json -count=1 ./...`; package outcomes lack independent file labels |
| Hono | `73ff6c0e82d66468e28ed439481220f56ab03882` | `npm test`; optional runtime/toolchain matrix excluded |
| FastAPI full-stack | `7257e606cb94eada1dca40173e2b7f99dd549bc6` | Declared native suites; Python 3.14/Bun/services/browsers unprovisioned |

Click combines floating-point open-boundary clamping with a new exact-integer test above 2^53. Chi
combines removal of True-Client-IP support with a new regression test. Each branch independently
passed its complete native suite; each nonconflicting combination failed. These two controlled
challenges and a passing source control were added for this evaluation; existing failing/docs
controls were reused. They are transparent probes, not a blind holdout.

Source-guarded revalidation checks tracked files and HEAD before/after suites; untracked additions
and temporarily restored edits are outside that guard. Paired artifacts require matching image,
revision, branches and candidate tree; original timings remain intact. A supplementary file-oracle
failure does not erase a valid native-suite observation.

## Detection and timing results

Counts use the first run per mode; repeats add timing samples, not defects. All failed mutations
include the integration challenge; independently failing source mutations were detected 1/1 in both
Click and chi, in every mode.

| Artifact / mode | Integration | All failures | False PASS | False FAIL | False BLOCK | File recall | Precision |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Click baseline targeted/balanced | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/129 |
| Click baseline full | 1/1 | 2/2 | 0/2 | 0/2 | 2/2 | 2/2 | 2/172 |
| Click fixed targeted/balanced | 1/1 | 2/2 | 0/2 | 0/2 | 1/2 | 2/2 | 2/129 |
| Click fixed full | 1/1 | 2/2 | 0/2 | 0/2 | 0/2 | 2/2 | 2/172 |
| Chi targeted/balanced | 1/1 | 2/2 | 0/2 | 0/1 | 1/1 | unavailable | unavailable |
| Chi full | 1/1 | 2/2 | 0/2 | 0/1 | 0/1 | unavailable | unavailable |

Click's native inventory has 43 files, all inventoried by Radar, with zero missing/unsupported files
within that collected suite. Source mutations select all 43. Chi inventories 19 and selects 15 for
its integration challenge (21.1% file reduction); independent file recall/precision remain
unavailable.

| Integration case / mode | Gate first/warm s | Merge + native s | Separate analysis s | Selected execution first/warm s | Process RSS first/warm KiB |
| --- | --- | --- | --- | --- | --- |
| Click fixed targeted | 54.20/67.58 | 12.66 | 4.76 | 11.78/16.18 | 141200/141248 |
| Click fixed balanced | 64.03/56.40 | 12.66 | 6.28 | 15.75/13.65 | 141360/141196 |
| Click fixed full | 54.33/55.88 | 12.66 | 5.06 | 12.54/14.06 | 141128/141160 |
| Chi targeted | 105.54/105.44 | 27.89 | 1.10 | 26.19/26.19 | 132348/131924 |
| Chi balanced | 107.56/107.39 | 27.89 | 1.53 | 26.54/26.60 | 132200/128480 |
| Chi full | 111.36/111.28 | 27.89 | 1.57 | 27.32/27.59 | 131772/129024 |

Both gates were slower than conventional merge + native suites. First/warm native baselines were
Click 16.91/16.25 s and chi 52.65/26.19 s; guard-only revalidation separately measured 13.91 s and
62.47 s. Click passing-source controls now pass every mode; full docs passes, targeted/balanced docs
still block. Baseline/fixed binaries include concurrent attribution changes: timing changes cannot
be assigned solely to one fix. Attribution reproduces the combination but does not prove causal
ownership; rerun durations are unavailable.

## Unavailable ground truth

Hono's corrected evaluation took 1535.14 s, preserving tracked source/HEAD. Its unchanged baseline
failed a logger assertion (`493ms` versus expected `/1s/` after a 1000 ms sleep), stopping the
native chain after 126.235 s. Host clock instability is plausible, not proven. Passing
docs/equivalent-source controls and failing URL mutations do not repair the baseline: all three
accuracy rows remain ineligible, with detection/error/recall/precision null rather than zero.

All nine Hono gates blocked on matching candidate trees because checkout-local node_modules were
absent from candidates. Targeted source selection was 84/118 (28.8% file reduction), balanced/full
118/118; missing inventory and independent file labels are unavailable. No recognized selected
execution time was observed. URL-mutation gate times were 30.62/27.30/28.17 s
(targeted/balanced/full), versus 105.22 s native merge + suite; separate analysis was
38.48/34.06/32.27 s and process RSS 146748/112024/102672 KiB. No warm repeat or successful cold
baseline. The full-stack case adds one unavailable row for dependencies and a rejected symlink. Four
projects are represented; only two qualify for controlled accuracy.

## Confirmed fixes and remaining boundaries

External validation exposed three correctness fixes: generated `.pytest_cache` components no longer
invalidate clean source evidence (tracked cache changes, sibling names and new source still do);
recognized successful Jest/Vitest runs resolve only matching planning-time runner uncertainty;
workspace `last` uses atomic timestamp reservations/publication rechecks under backward/repeated
clocks. Regressions retain missing-runner, zero-case, omission and concurrent-publication cases.
Oracle parsing rejects zero-test pytest success and distinguishes missing modules from source import
failures; pairing rejects source mismatch/output aliases.

Docs-only execution remains blocked; central import hubs overselect. Dynamic imports/computed
paths/unsupported resolution need independent challenges. `full` means recognized inventoried
commands, not all upstream tests. Go compiler mutation accuracy and independently collected non-Click inventories are unavailable. Candidate dependencies are not installed/transplanted
automatically; full-stack needs an offline service/browser image and Hono needs a characterized
baseline.

Containers were non-root/offline with read-only inputs/root, dropped capabilities and resource
limits. Images/input/binary hashes are recorded; mutable Debian repositories prevent bit-identical
rebuild claims. First baseline calls used new containers without flushing host page cache; gates
followed oracle runs, so cold gates are unavailable. Warm repeats reuse tool caches with fresh
candidates. Concurrent workloads limit timing comparisons. RSS is per process, not aggregate
container memory; selected durations exclude attribution reruns, leaving total execution/residual
phase time unavailable. File reduction is not runtime savings.

## History replay

[`benchmarks/history/replay.py`](../benchmarks/history/replay.py) replays real merges from a
local clone through the static gate. For a merge with parents P1 (base line) and P2 (merged work)
it gates `[P1, P2]` onto `merge-base(P1, P2)`, so whatever landed on the base meanwhile is one
branch. Squash merges need a fetched `refs/pull/N/head`. Commits within a window (default 48 h)
that touch files from both sides are recorded as follow-ups, and Alembic or Django merge
migrations as signals. Nothing is fetched and no repository code runs.

```sh
python3 benchmarks/history/replay.py mine CLONE --base main --out /tmp/candidates.json
python3 benchmarks/history/replay.py replay /tmp/candidates.json --radar /tmp/radar --out /tmp/results.json
python3 benchmarks/history/replay.py report /tmp/results.json --labels labels.csv
```

Follow-ups are a heuristic: they mix causes and miss breaks fixed later or elsewhere. Causes need
a person's label (`symbol`, `migration`, `lockfile`, `generated`, `route`, `runtime`, `other`,
`none`), so samples stay small. A FAIL on a merge labeled `none` counts as a false failure.

## Evidence and historical verification

[Summary JSON](../benchmarks/external/results/summary.json),
[raw hashes](../benchmarks/external/results/raw-artifacts.json) and
[artifact guide](../benchmarks/external/results/README.md) retain lossless results,
including superseded/unavailable attempts. Readable final reports:
[Click baseline](../benchmarks/external/results/paired/click-baseline.md),
[Click fixed](../benchmarks/external/results/paired/click-fixed.md),
[chi](../benchmarks/external/results/paired/chi.md),
[Hono](../benchmarks/external/results/hono-fixed.md),
[full-stack](../benchmarks/external/results/baseline/fullstack.md).

[Verification provenance](../benchmarks/external/results/verification-source.json) records commit
`e340bbfc57e00c3e641225683c11a36b57107363`, tree `44302bb9ad3beddd8ce28598d59057213b441632` and
byte-checked Git archive extraction. Historical Linux mirror checks passed full/race Go tests, vet
and native build; mounted WSL tests also completed. Harness regressions (14 existing, 31 external),
GitHub integration (8), workflow (5), release (2), smoke and selection demo passed. These are
snapshot observations, not verification of a later release. Hosted CI, Windows execution and
provisioned full-stack runtime were not verified.
