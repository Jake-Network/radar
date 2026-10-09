# Radar 0.4 reproducible evaluation

`benchmarks/evaluate.py` evaluates controlled changes in private Git repositories.
It reads local pinned checkouts; it does not clone, install dependencies, fetch,
or run a shell command. Static analysis is the default. Original repositories,
branches and worktree changes are preserved.

## Reproduce

```sh
go build -o /tmp/radar-0.4 ./cmd/radar
RADAR_BENCHMARK_BIN=/tmp/radar-0.4 python3 -m unittest discover -s benchmarks -p 'test_*.py'
python3 benchmarks/evaluate.py benchmarks/manifests/integration.json \
  --radar /tmp/radar-0.4 --allow-execution --out /tmp/radar-integration-results
GOCACHE=/tmp/radar-go-cache python3 benchmarks/evaluate.py benchmarks/manifests/monorepo.json \
  --radar /tmp/radar-0.4 --allow-execution --out /tmp/radar-monorepo-results
python3 benchmarks/evaluate.py benchmarks/manifests/click.json \
  --radar /tmp/radar-0.4 --out /tmp/radar-click-static-results
```

Executable fixture cases are checked-in, trusted Radar code. Explicit
`--allow-execution` enables their tests. A manifest cannot make an arbitrary
external checkout trusted by declaring it a fixture. External execution also
requires `--isolation-wrapper '["/absolute/path/to/operator-wrapper"]'` and
`--isolation-attestation 'description of enforced boundaries'`. The wrapper
receives the command as separate arguments and must enforce filesystem,
process, credential and network isolation. It encloses both independent full
suite commands and Radar itself when `gate --run` executes repository tests.
An attestation is recorded; the harness cannot validate or certify the supplied
sandbox. No external runtime tests were executed for the static reports.
A temporary checkout alone provides no security isolation.

## Manifest

The version 1 schema uses `repository.name`, optional `url`, and either a
checked-in `fixture` path or `local_checkout` plus a full 40-character `revision`.
Paths are relative to the manifest unless absolute. External files come from
`git archive REVISION`; Git hooks, replacement objects and lazy fetch are
disabled. The private baseline is a synthetic commit: reports preserve the
original pinned SHA, original tree and synthetic commit/tree mapping.

`cases` have an `id`, category, prose `ground_truth`, optional `defect_expected`,
and `agents`: one list of edits per independent branch. Each edit contains
`path`, literal `find`, literal `replace`, and optional `all`. Paths are confined
to the private repository. Missing patterns become retained unsupported rows.
Each branch starts at the same base. Ordinary Git merges combine the branches.
The integration fixture changes producer price and client quantity: each branch
passes the existing budget assertion, but their combination fails it.

`full_suite` contains independent command records:

```json
{"cwd": ".", "argv": ["python3", "-m", "unittest", "test_checkout"],
 "test_files": ["test_checkout.py"], "recognizer": "unittest"}
```

Available recognizers are unittest, node, go, pytest, cargo, Jest and Vitest.
Go requires actual test pass/fail JSON events (`go test -json`) or verbose
test-result markers; package-only success is unavailable test evidence. Cargo
requires a positive passed-plus-failed count; zero-test success is unavailable. To measure
failing-file ground truth, define commands per independent test file or package.
A grouped command failing does not identify every file inside the group:
reports explicitly treat those as test-file groups. For precise pytest failing
file recall, split the suite into per-file commands rather than attach all files
to one invocation. An empty `test_files` list preserves runtime status but makes
file recall and full-suite selection fraction unavailable. No-result commands,
missing executables, unsupported recognizers, timeouts and output limits are
separate states. Output is file-backed, polled and capped at 16 MiB retained
stdout plus stderr per command; oversized output is never consumed as successful
evidence. Temporary disk output can exceed that cap between polls. POSIX
execution kills the process group on timeout/limit/completion; on Windows the
fallback kills the direct process only, so complete descendant cleanup is
unverified and an external isolation wrapper remains necessary.

## Evidence and metrics

JSON reports retain command argv, cwd, exit code, stdout/stderr, recognized
results, duration, per-command RSS when GNU time is available, Radar binary
SHA256, harness source revision, platform and tool versions. Static `gate`
records the candidate verdict/provenance and proposals; static `check --suite
balanced` measures actual selected commands with the same default command budget.
Runtime `gate --run` records executed commands and recognized observations.
Discovery, proposals, selection, execution and recognized failures remain
separate fields. A static gate passing supplies no runtime evidence.

Every aggregate fraction includes numerator, denominator and value. A zero
eligible denominator produces `null`. The metrics are:

| Metric | Observation |
| --- | --- |
| Integration defect detection | Candidate gate fails / failing combined candidates with at least two independently passing branches |
| Ordinary mutation defect detection | Candidate gate fails / independently failing full-suite mutation candidates |
| Known failing test recall | Ground-truth failing file groups with recognized failing Radar execution / ground-truth failing groups |
| False positive and false block | Failing or blocked gates / independently passing full-suite candidates |
| Uncovered change rate | Selection-reported uncovered files / changed files for measured selection rows |
| Selection precision | Selected file groups observed failing / selected files in observed candidates |
| Selection recall | Ground-truth failing file groups selected / ground-truth failing groups |
| Selected relative to full suite | Selected files / explicitly enumerated full-suite files |
| Analysis overhead | Actual static gate plus static selection probe wall time |
| Total verification time | Actual runtime gate wall time, including its analysis and execution |
| Peak memory | GNU time maximum RSS for each measured command; not simultaneous aggregate process-tree memory |

The baseline is ordinary Git merge plus `baseline_suite` commands. The trusted
monorepo defines grouped unittest, Node and Go `-json` invocations, avoiding per-file
process overhead in the CI comparison. If `baseline_suite` is omitted, CI comparison time is unavailable; the
independent per-file oracle still runs and retains its timing separately.
Baseline wall time includes merge and tests. The per-file independent ground-truth
suite runs separately; its extra measurement cost is recorded. The harness additionally checks the
passing baseline and individual branches; their raw observations are retained.
Those setup measurements are not hidden as a claim of faster integration.
Selection precision is a lower bound on useful selection: passing tests can be
relevant. Declared expected defects are hypotheses, not measured outcomes.
`no_defect_observed` means existing tests passed, not that no defect exists.
`defect_not_detected`, `no_relevant_test_executed`, `environment_unavailable`,
`analysis_unsupported` and `ground_truth_unavailable` have distinct meanings.
Unavailable samples remain in the report and unavailable-sample aggregate.

## Results and limits

See `benchmarks/results/` for genuine JSON and Markdown runs. The integration
fixture directly demonstrates the core two-agent failure. The existing
selection monorepo exercises nested Python roots, ESM imports, Go packages,
shared schema contracts and runtime file reads. Its historical mutations were
used while improving Radar and are not a held-out accuracy study. Current runs
measure current gate policies; historical selection-only results are unchanged.

Pinned public repository runs are static evidence unless their JSON includes
independent full-suite observations and a runtime gate. Click's central barrel
imports cause a source mutation to select all 43 inventoried test files; this
adds selection cost without establishing a runtime saving. External detection,
false-blocking, failing-test recall and full-CI timing remain unverified until
suitable isolated environments and independently enumerated suites are supplied.
Single local observations do not establish production recall, confidence
intervals, safety or performance. JSON raw timing should be used for comparisons;
no speed claim is made.

The integration metric only includes at least two individually recognized passing
branches whose combined full suite fails. Ordinary single-branch defects are
reported separately as mutation detection, even when Radar finds them.

The local trusted monorepo run detected 11 of 11 failing mutations and recognized
all 16 ground-truth failing file groups. Balanced selection chose 48 of 156
full-suite file observations across its 12 cases. Its documentation-only change
passed the independent full suite but the default runtime gate blocked because
no tests were selected/executed: the measured false-block rate in that one clean
monorepo case is 1/1. This is a policy cost, retained alongside favorable rows.
The separate clean source case in the integration fixture passed. Small, tuned
fixture denominators do not justify a public general accuracy claim.

## Public static corpus acquired for 0.4

Each project has a controlled source mutation grounded in an upstream test or
behavior, plus a documentation control. Expected defects are hypotheses;
external tests were not run. Selection rows are observations, not execution
savings or detection accuracy. External no-defect rates remain unavailable.

| Project / pinned commit | Source case selected / discovered | Observed limitation |
| --- | --- | --- |
| FastAPI `40e33e492dbf4af6172997f4e3238a32e56cbe26` | 485 / 485 | Central routing edit selects whole discovered suite; runtime dispatch unobserved |
| Hono `73ff6c0e82d66468e28ed439481220f56ab03882` | 118 / 118 | Shared utility, exports map and nested runtime roots; no runtime evidence |
| tRPC `85841a1ae4679847fd29ad5454c1a584a2e206d2` | 23 / 183 | Workspace aliases, barrels and generated/type-test configuration remain bounded relationships |
| go-chi/chi `67be7d9cafdaeb4e04e887ff78d09e030ee43b00` | 16 / 19 | Selected package companions do not prove routing semantics |
| tokio-rs/axum `fe56a310efbb30b3b178059c48a31d1000ed5f62` | 24 / 168, partial check | Gate refuses committed README symlink; both samples retained as unsupported |
| Pallets Click `2247b35ea1c47c727d7a06e51fa280e12a863ff6` | 43 / 43 | Central barrel imports select whole discovered suite |

All documentation controls selected zero test files. Axum's partial `check`
selection remains separate from its unsupported candidate `gate`. These counts
do not establish complete inventories for framework/build-specific tests.

Acquisition (public source only, no dependency preparation or tests):

```sh
GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -c core.hooksPath=/dev/null clone --depth 1 --branch 0.115.0 https://github.com/fastapi/fastapi.git /tmp/radar-04-fastapi
GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -c core.hooksPath=/dev/null clone --depth 1 --branch v4.6.0 https://github.com/honojs/hono.git /tmp/radar-04-hono
GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -c core.hooksPath=/dev/null clone --depth 1 --branch v11.0.0 https://github.com/trpc/trpc.git /tmp/radar-04-trpc
GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -c core.hooksPath=/dev/null clone --depth 1 --branch v5.1.0 https://github.com/go-chi/chi.git /tmp/radar-04-chi
GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null git -c core.hooksPath=/dev/null clone --depth 1 --branch axum-v0.7.7 https://github.com/tokio-rs/axum.git /tmp/radar-04-axum
```

Tags locate commits; immutable SHAs in manifests determine analyzed source.
The harness archives the specified commit even if the checkout tip differs.
Review the table against `git rev-parse HEAD` after acquisition; annotated tag
object hashes are not commit hashes. Prepare Click using its existing pinned
recipe in [VALIDATION_SELECTION](VALIDATION_SELECTION.md), without running tests.

```sh
for project in fastapi hono trpc chi axum; do
  python3 benchmarks/evaluate.py "benchmarks/manifests/$project.json" \
    --radar /tmp/radar-0.4 --out "/tmp/radar-$project-static-results"
done
```

Reports retain actual timing/RSS. Other validation processes were active during
these runs: repeat under controlled load before making performance claims.
Some reports reaggregate corrected metric definitions from unchanged observations;
`report_reaggregation` identifies that reducer hash and reason.
