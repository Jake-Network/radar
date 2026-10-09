# Radar 0.4 validation record

## Baseline and reproduced defects

Review baseline: `4e9ffdb9fb33e91c3ac25d2d5168bec63e770121`.
Initial checkout: `main` at `4b1ec11effa6f3f10d4b561318d68e9e82653bfb`,
the merge containing that baseline. Work continued on local
`radar-0.4-reliability` created from `verification-trustworthiness`.
Unrelated untracked `.serena/` was preserved.

Before fixes, these new executable regressions failed:

```
go test ./internal/cli -run 'TestGateDefaultRequiresSelectionEvidence|TestGateInterspersedFlags' -count=1
```

* A committed `model.py` change and unrelated `other.py` change ran one real
  unittest successfully, reported `test_selection: incomplete` and
  `uncovered_changes: [other.py]`, yet returned gate `pass`, exit 0.
  Root cause: default executed policy required execution but not selection.
* `gate feature/model --run` returned exit 2 while `gate --run feature/model`
  ran the test. Root cause: Go FlagSet stops at its first positional argument.

Already fixed at baseline: committed-ref pinning, contract obligation retention,
candidate source mutation invalidation, required runner/budget omissions,
bounded working-source/config fingerprints, full-suite inventory accounting and
manual release publication safeguards. These protections were retained.

## Trust defaults and false blocking

Balanced selection now requires selection completeness. The existing checkout
fixture reads `frontend.ts` at runtime from Python; its frontend-only branch
therefore blocks under balanced selection despite the full discovered unittest
passing. This is a measured false-block example, not evidence of a defect in that
branch. Its full-inventory run passes, while the combined runtime test fails.
The smoke explicitly uses full selection for individual branch observations.
This small fixture result is not an estimate of real-repository false-block rate.

Feature-branch commits and pushes do not constitute a `0.4.0` release.
No tag, merge, rebase or release publication is part of this milestone.

## Milestone 1: gate reliability

Changes: `internal/integration/integration.go` default suite requirements;
`internal/cli/gate_flags.go`, `cli.go`, `gate.go` parsing/presentation;
`internal/git/worktrees.go` read-only status; `configuration.go`, `check.go`,
`merge_check.go` exact invocation inputs; byte parsers in `internal/gate` and
`internal/planning`; bounded MCP worktree summary in `mcp.go`.

Regression files: `gate_reliability_test.go`, `gate_scenarios_test.go`,
`configuration_test.go`. All use real Git candidates where appropriate;
A–F run under default and equivalent explicit policies, G tests intentional
limited-policy behavior. They assert verdict/exit, checks/coverage, inventory,
selection, actual recognized tests, JSON and human output. Four worktrees verify
staged/unstaged/untracked/ignored/detached reporting and unchanged Git status.
Executable candidate tests modify the selected caller policy while passing:
the gate correctly blocks rather than combining incompatible evidence.
Copied shell commands for valid `#feature` and `feature/{left,right}` refs are
also tested; the prior quoting omitted shell comment/brace semantics.

Focused tests passed, as did CLI/integration/selection/gate/Git package tests.
Existing source/config mutation, immutable-ref, contract obligation and MCP
authorization tests remain enforced. Successful custom policies keep their
scope; additive JSON fields are `worktrees`, `configuration_inputs` and the
configuration check. Default suite-based gates now fail closed on selection gaps;
explicit command verification and selected custom policies retain their meaning.

Limits: no uncommitted candidates, no behavioral coverage claim, no transient
write detection between boundaries, no OS snapshot lock, no automatic repair.
Missing runner before execution is blocked; actual execution errors/timeouts
remain error observations. Documentation-only `--run` can block for lack of
recognized execution; this cost is measured below rather than concealed.

## Milestone 2: continuous and native validation

CI previously omitted explicit unit/PR-driver/metadata/workflow checks and a
Windows installer job. actionlint reproduced `runner.temp` being unavailable
in the sample workflow's job-level `env`; the report path now uses step `env`.

Changed `.github/workflows/ci.yml`, the release Unix installer condition,
`.github/actionlint.yaml`, `integrations/github-actions/radar-pull-request.yml`,
`scripts/workflow-test.py`, release metadata/validation/parser-smoke scripts
and portable Unix installer tests. Workflow controls use pinned Actions,
read-only defaults, credential-free checkouts and explicit protected publication.
README/CAPABILITIES/ROADMAP/RELEASING/SECURITY now agree on native Windows build
definitions, manual publication and Homebrew formula generation without a tap push.

Executed successfully on Linux:

```
RADAR_TEST_BIN=/tmp/radar-04-final python3 integrations/github-actions/test_workflow.py
python3 scripts/release-test.py
python3 scripts/workflow-test.py
/tmp/radar-ci-tools/actionlint -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/release.yml integrations/github-actions/radar-pull-request.yml
bash scripts/install-release-test.sh
GOCACHE=/tmp/radar-04-gocache python3 scripts/release.py /tmp/radar-04-final-release 0.4.0-dev
python3 scripts/validate-release.py /tmp/radar-04-final-release 0.4.0-dev
```

Outcomes: PR driver 8 tests; metadata 2 tests spanning all five archive formats;
workflow security 5 tests; all three workflows lint; hostile installer archives,
version mismatch and preservation-on-failure checks pass. Built/unpacked native
artifact checks SHA256, exact VERSION, LICENSE/dependency notices and actual
Python/TypeScript/Go/Rust parsed symbols, index counts and essential gate behavior.

| Platform | Native build/executable | Installer | Hosted validation |
| --- | --- | --- | --- |
| Linux amd64 | Passed locally, Go 1.26.8/GCC 13.3/glibc 2.39 | Passed locally | Unverified |
| Linux arm64 | Configured; unverified | Format tested, native unverified | Unverified |
| macOS amd64/arm64 | Configured; unverified | Portable regression configured, native unverified | Unverified |
| Windows amd64 | Configured UCRT64; unverified | Native CI configured; PowerShell unavailable here | Unverified |

Local archive: `/tmp/radar-04-final-release/radar-linux_amd64.tar.gz`, SHA256
`093891fdc520c9a4ef11a9c824a039ae8f98ece35446d1997f3922f74610cf78`.
It inherits local glibc 2.39; it is not evidence for the hosted Ubuntu 2.35
compatibility baseline. Go emitted a read-only home module-stat-cache warning;
packaging and installed smoke still completed with exit 0. Native formats
tested on Linux are not executable qualification for another OS/architecture.

## Milestone 3: reproducible evaluation

The old selection evaluator counted selected failing files and estimated selected
execution duration; it did not measure executed gate detection or full-CI cost.
It also modified the supplied external checkout. It remains a historical
selection-only study. New `benchmarks/evaluate.py` uses private committed copies,
explicit controlled independent branches, pinned external source, positive
recognized test results, grouped CI baselines, an independent per-file oracle,
bounded subprocess output, timing/RSS and unavailable-sample denominators.

Regression tests cover actual two-branch failure, ordinary single-agent failure
excluded from integration detection, no fabricated CI comparison, absent runners,
no recognized results, unsupported recognizers, untrusted execution rejection,
output/time bounds and partial selection after unsupported gate analysis.
CI runs executable checked-in fixtures; no external code is implicitly executed.
Raw JSON and Markdown results are in `benchmarks/results/`; exact commands,
binary/harness/manifest hashes, upstream SHA/tree and synthetic candidate mapping
are preserved. See [BENCHMARKS_0_4](BENCHMARKS_0_4.md) for metrics and reproduction.

Measured scope: one core two-agent failing combination, 12 existing tuned
monorepo mutations, and static controlled changes on six pinned public projects.
External runtime detection/recall/false-block rates and full-CI timing remain
**incomplete**. Acquisition was permitted; external tests were not run.
Axum's committed README symlink makes candidate preview unsupported, preserved
alongside partial read-only test selection. No repository was modified to force
it into the supported set.

## Milestone 4: first-use behavior and next languages

Terminal output labels bounded policy passes, static passes, required checks,
inventory/selection/actual recognized executions, uncovered paths, blocked next
steps and excluded worktree changes. JSON schemas retain existing fields with
additive provenance. README leads with individually passing branches that fail
together and provides the isolated checked-in smoke command. No new orchestrator,
telemetry, cloud access or LLM key was introduced.

All five `make demo-all` scripts and the integration gate smoke passed.
The intelligent-verification demo required host execution because restricted
socket creation blocked its localhost HTTP server; that environmental failure
was resolved without changing tests. Actual pinned FastAPI/Pydantic/httpx fixture
qualification also passed with three recognized source-intact tests, after
preparing its explicit environment (offline cache was initially unavailable).
These are Radar fixtures, not execution of downloaded external repository tests.

[LANGUAGE_EXPANSION](LANGUAGE_EXPANSION.md) recommends Java first based on the
fit with module/contract/JUnit evidence, with a separate C/C++ configuration-aware
design. Neither language was implemented. [Changelog](RADAR_0_4_CHANGELOG.md)
summarizes the development milestone.

## Final verification

All required commands completed with exit 0 against the final Go implementation:

```
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/radar
RADAR_BENCHMARK_BIN=/tmp/radar-04-final python3 -m unittest discover -s benchmarks -p 'test_*.py'
```

Benchmark regressions: 12 passed. The final unit and race runs included the real
Git/worktree/executable gate regressions. A separate diagnostic run with a
60-second CLI timeout exceeded that timeout under concurrent load; the normal
required runs and the 120-second diagnostic run passed. Formatting and
`git diff --check` are clean. The standalone final executable SHA256 is
`caa84a969dab0f7c24516795d55a9c8aa9e8d10f31273c7ea35a184966421e31`.

| Observed corpus | Detection and execution | Cost and limitation |
| --- | --- | --- |
| Core two-agent fixture | 1/1 eligible integration defect; 1/1 failing test-file group executed and recognized | Median baseline full CI 0.11465 s; runtime gate 0.29559 s. Radar was slower. |
| Tuned monorepo fixture | 11/11 failing mutations; 16/16 failing test-file groups recognized; 48/156 inventory files selected across cases | No eligible two-agent integration denominator. Documentation-only false block 1/1. Median baseline 0.68135 s; gate 0.54443 s, one local run. |
| Six independent public projects | Static source-change selection: Click 43/43, FastAPI 485/485, Hono 118/118, tRPC 23/183, chi 16/19; Axum partial check 24/168 | No external runtime ground truth or CI comparison. Axum candidate analysis unsupported because of committed symlinks. |

File-group recall is not test-case coverage. These small controlled corpora do
not establish a general detection rate or speed advantage. External summary
reducers were refreshed from unchanged raw observations; their reducer hash and
reason are recorded separately from the original execution provenance.

At the time of validation, changes were local on `radar-0.4-reliability` and no
commit or push had been performed. Subsequent user-authorized branch publication
does not change the qualification limits above; unrelated `.serena/` is excluded.

## Review follow-up: worktree and configuration compatibility

The preceding executable hashes and measurements record the initial milestone
validation. This follow-up changes the implementation without replacing those
historical benchmark observations.

Reproduced before fixing: a Git shim rejecting only `worktree list --porcelain
-z` made both static and executed gates exit 2; real candidate tests mutating
the selected policy or approved plan caused a blocked gate but still wrote a
passed execution artifact. New executable regressions failed on these behaviors.

| Review item | Correction |
| --- | --- |
| 1 | Worktree-list failure becomes `worktree_inspection_error` plus human/MCP warning; committed verification keeps its verdict and exit code. No unsafe path-parsing fallback. |
| 2 | Skip bare entries; inspect only real linked worktrees. |
| 3 | Read global excludes as configuration data, including per-worktree `includeIf`, then selectively apply the ignore path. Local setting precedence and disabled fsmonitor remain intact. |
| 4 | Actual changed configuration strips reusable plan records, rehashes execution IDs and suppresses evidence-file persistence, with a reported explanation. Actual test results remain visible. |
| 5 | Accept bounded stdin/pipes; capture once and report stream stability unknown. An explicit stability requirement still blocks unknown evidence. |
| 6 | No configuration check without selected inputs. Missing required checks remain requirements, not synthesized observations. Actual derived contract checks are retained for consistent reevaluation. |
| 7 | Static gates blocked by configuration changes suggest the same static gate, preserving selected flags. |
| 8–9 | Remove unused policy loader; share parser size constants and perform one size validation after bounded reads. |
| 10 | Use standard `slices.Contains`; inspect at most four worktrees concurrently while preserving deterministic listing order. |

Regression coverage: five actual-Git worktree tests; three CLI review tests
(legacy-option warning/compact MCP, real approved-plan evidence rejection for
both plan and policy mutation, static rerun); six additional configuration tests
(absent inputs, missing requirements, stdin CLI, one-shot plan pipe, size bounds,
plan-record invalidation and execution ID). Existing strict-completeness tests
caught an intermediate exit-path regression; its original behavior was restored
without weakening the test.

The Git version compatibility test simulates the older unsupported option on
the installed Git; no native Git 2.25 binary or additional OS was exercised.
Worktree inspection failure is not a clean-worktree claim. Unknown stream
stability is not filesystem snapshot isolation. No worktree, source or artifact
is automatically repaired.

Follow-up final verification passed (exit 0):

```
GOCACHE=/tmp/radar-04-gocache go vet ./...
GOCACHE=/tmp/radar-04-gocache go test ./...
GOCACHE=/tmp/radar-04-gocache go test -race ./...
GOCACHE=/tmp/radar-04-gocache go build -o /tmp/radar-review-final ./cmd/radar
RADAR_TEST_BIN=/tmp/radar-review-final python3 integrations/github-actions/test_workflow.py
RADAR_BENCHMARK_BIN=/tmp/radar-review-final python3 -m unittest discover -s benchmarks -p 'test_*.py'
bash scripts/smoke.sh /tmp/radar-review-final
```

Final CLI unit/race durations were 103.039/108.110 seconds; no remaining test
failure or race report. PR-driver tests: 8 passed; benchmark regressions: 12
passed; integration smoke preserved individually passing branches and a failing
combined candidate. Formatting and `git diff --check` are clean. Build completed
despite the existing read-only home module-stat-cache warning. Follow-up binary
SHA256: `1da9c53d5453fdd560745b4d839faba280280c975fbf72679d78c613341c971d`.
