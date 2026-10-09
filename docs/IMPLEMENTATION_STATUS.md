# Implementation status

Verified locally on 2026-10-09 with Go 1.26.8, linux/amd64 (WSL workspace). Radar 0.2.0 extends the bounded first public MVP with the agent and team workflow milestone. This is local implementation and release preparation, not a claim that all runtime architecture is understood or that a public release has been published. This document includes historical snapshots; current local work does not publish releases or mutate remote state.

## Milestone acceptance

| Milestone | State | Executable acceptance evidence |
| --- | --- | --- |
| 0 Foundation | Complete for the initial release | Go CLI, configuration, transactional/versioned SQLite snapshots, unified graph, immutable Git inspection and actual four-language indexing/query |
| 1 Architecture MVP | Complete for the documented structural and explicit-contract scope | Grounded context, versioned plans, agent skills, proposed graph overlay, contract projections, task DAG/packets and deliberately inconsistent architecture rejection |
| 2 Contract MVP | Complete for the supported schema subset | Real Python producer / explicitly linked TypeScript consumer branches, directional compatibility checks, explanation, independent-change negatives and working-tree/checkpoint distinction |
| 3 Continuous verification | Complete for the declared deterministic rules | Reviewed plan digest, immutable baseline/implementation ancestry, approved-contract comparison, actual test evidence, stale-evidence rejection and task/agent feedback |
| 4 Agent and team workflow (0.2) | Complete for the scenarios below | Location-independent IDs, worktree-shared state and evidence, inferred import dependencies, `affected`, robust contract comparison, environment-error classification, more harnesses, assumption lifecycle, MCP server and Claude Code/GitHub integrations |
| 5 Expansion | Deferred | Compiler semantics, SCIP, tsconfig aliases, Protobuf, incremental parsing, large-repository optimization and interactive visualization |

## Historical v0.2 validation snapshot

The checks and live-agent results below describe the prior implementation snapshot; they are not fresh live-agent or hosted-CI claims for the current milestone.

### Executed checks

- `go test ./...`: **127 passed test/subtest cases across 16 tested packages**, zero failures or skips. The entry point has no standalone tests.
- `go test -race ./...`, `go vet ./...`, `gofmt -l cmd internal` (empty): passed.
- `go build -o bin/radar ./cmd/radar`: passed. Both `examples/organization/demo.sh` and `examples/verification/demo.sh` exit 0 against this binary, including their expected-failure assertions.
- `scripts/release.sh` produced a linux/amd64 archive including licenses (now with gopkg.in/yaml.v3), schemas and integrations.
- Generated plans, the example plans and recorded evidence validate against `schemas/plan.schema.json` and `schemas/evidence.schema.json` (jsonschema 4.10, draft 2020-12).
- `.github/workflows/ci.yml` ran to success with `act` 0.2.89 in the `catthehacker/ubuntu:act-latest` image (setup-go, gofmt, vet, race tests, build, both demos, release packaging).

## End-to-end runs with real tools

| Area | Setup | Result |
| --- | --- | --- |
| Claude Code MCP | Claude Code 2.1.295 `claude -p` with `--mcp-config` (`radar mcp --root .`) on a copy of the organization fixture | Server `connected`; 14 tools listed (no `test`/`approve`); doctor, init, index, resolve, affected and contracts calls answered correctly. The first run exposed that `radar_index` returned the full 66 KB snapshot; it now returns a summary and tool output is capped. |
| Claude Code Stop hook | Plan requiring `backend/exports.py`, hook from `integrations/claude-code` | The agent tried to stop, received the failed check, created the file, and stopped once verification passed. |
| jest 29.7 / vitest 2.1 | Untracked `node_modules` declared with `link` | Passed and failing commits recorded as `passed`/`failed` with counts; vitest JUnit via `junit`; without `link`, `error` (not a test failure). |
| pytest 9.1 | uv-created `.venv` declared with `link` | `passed` (2 run, 1 skipped), JUnit variant, failing commit `failed`; system Python without pytest yields `error`. |
| cargo 1.99 | rustup toolchain and `itoa` dependency fetched beforehand, `cargo test --offline` | `passed` (2 run, 1 ignored); failing commit `failed` (exit 101). Without CARGO_HOME/RUSTUP_HOME the 0.1.0-style environment fails with `rustup could not choose a version of cargo`, now classified `error`. |
| GitHub pull-request workflow | Example workflow's `run:` steps executed with `bash -eo pipefail` against a bare origin with `refs/pull/{1,2,3}/head` and a `gh` stub | Lint warns about the stale declared field, `affected` maps the contract, impact emits one `::error` annotation (exit 1), scan reports the PR itself and the PR that edits the consumer, not the unrelated PR. A hosted GitHub runner was not used. |

The pull-request run also led to two behavior changes: a removed property no longer produces a second `required_removed` finding, and `scan` reports a pair of branches only when the consumer branch changed the binding or its consumer file (an untouched branch adds nothing beyond the producer's own finding).

## Regression scenarios for 0.2

Each was reproduced against 0.1.0 before the fix and is now covered by a test.

| Problem in 0.1.0 | 0.1.0 result | 0.2.0 result | Test |
| --- | --- | --- | --- |
| A `format: uuid` annotation plus a removed `total` field | 0 findings, exit 0, one warning | `contract_field_removed` error, status `failed`, exit 1 | `TestAnnotationsDoNotDisableComparison` |
| `radar test -- go test -json ./...` on a module with an external dependency | `failed` (GOPROXY=off, empty module cache), no output shown | `passed` using the local module cache; failures before tests are `error` with an output tail | `TestGoModuleDependenciesResolveFromLocalCache`, `TestBuildFailureIsErrorNotTestFailure` |
| Same repository content in two checkout paths | Different entity IDs (`StableID(absolute path, ...)`) | Identical readable IDs | `TestEntityIDsAreReadableAndLocationIndependent` |
| Evidence recorded in a linked worktree | Rejected in the main checkout (path-bound) | Verifies authoritatively; worktree shares main state | `TestWorktreeSharesStateAndEvidence` |

Other covered behavior: import resolution for all four languages (`TestImportResolution`); `affected` depth and contract/task mapping; `resolve`; contract lint with stale consumer fields; nullable/enum direction rules, `allOf`, recursive `$ref`, YAML documents; setup/env/link/JUnit test execution and conflicting declarations; pytest/jest/vitest/node/cargo/unittest/go counts; assumption lifecycle and next steps; per-command flag rejection and help; human output; MCP protocol, tool list (no `test`/`approve`) and argument validation; storage v1→v2 migration and working-tree snapshot retention.

## Architecture and boundaries

- Package boundaries separate CLI, indexing (single loop with working-tree and commit providers), language parsing, import resolution, Git access, graph, explicit contracts, planning, verification, evidence and persistence. `pathutil` and `jsonptr` hold shared validation.
- Tree-sitter provides structural syntax evidence. File dependencies are inferred from import paths and labeled `inferred`; no verified CALLS graph is fabricated. Doctor reports semantic adapters unavailable even if external compiler tools are installed.
- Explicit JSON/YAML OpenAPI and JSON Schema bindings establish declared producer/consumer dependencies. Runtime access remains unverified. Unsupported constructs are compared locally; when they change, the report is `incomplete` rather than silently passing.
- Plans distinguish proposed, inferred and verified evidence. Consequential review is a local digest-bound declaration, not authenticated approval and never permission for Git mutation. Agents cannot run `test` or `approve` through MCP.
- Test execution requires explicit `--allow-execution`, runs declared commands against a private immutable snapshot, reuses local dependency caches offline and records bounded digest-based evidence. It is not an OS/network sandbox. Raw output is never persisted.
- Indexing replaces full snapshots and retains commit versions; the newest ten working-tree snapshots are kept per repository. Limits: 2 MiB per source file, 64 MiB aggregate source and 10,000 source files; exceeding the budget yields one repository-wide diagnostic and a partial index.
- Native Linux amd64 is the tested distribution target. Windows/macOS native builds and public distribution need their own execution evidence. Historical hosted CI success is recorded separately below; it does not validate later local changes.

## Current agent-native milestone

Working slices now add project-local `setup`, manifest-free `check`, conservative
`discover`, private combined-source `merge-check`, opt-in recognized test
observations with exact candidate evidence, read-only MCP integration tools,
planning ownership/integration warnings, bounded repair guidance, module-path
alignment and unpublished Linux release artifacts. The implementation plan and
trade-offs are in [the inspected plan](plans/AGENT_NATIVE_MILESTONE.md).

Current validation results are recorded in [the milestone report](MILESTONE_REPORT.md).
The [central demo](INTEGRATION_DEMO.md) proves independently passing branches can
merge textually yet fail combined verification, then pass after repair. A passing
observed command does not make the entire integration report complete.

Remaining scope: compiler semantics, runtime transport validation, imported
Python response models, broad TS client patterns, semantic contradiction detection in architecture prose, large-monorepo evidence,
multi-platform published binaries and signing. Existing plan/manifest/evidence
formats remain readable; combined evidence is a separate metadata format.

Next: expand scoped producer/consumer resolution and measure recommendation
precision while preserving explicit execution authorization. See
[performance measurements](PERFORMANCE.md) before planning cache redesign.


## Intelligent verification baseline and current work

The inspected baseline is commit `342e482`. Hosted GitHub Actions run
[`37884430377`](https://github.com/Jake-Network/radar/actions/runs/37884430377)
succeeded for the prior baseline, as verified during the baseline audit. It is historical evidence, not a hosted validation of these later
changes. No published GitHub Releases existed at that baseline check. Binary
installation remains contingent on a separately reviewed published release.

Current changes introduce selected `gate.verdict`/`coverage` reporting with
versioned policies, read-only `--suggest-tests` on check and merge-check, and
explicit recommended-suite execution with per-command CWD/evidence. The
integration suite has a shared timeout and rejects more than 16 selections.
Static recommendation reasons preserve inferred/proposed provenance and known
coverage gaps. Reviewed exact candidate-plan criteria are implemented and
regression-tested: matching argv/CWD/configuration and review digest are required;
branch/stale evidence, source mutation, missing environment names, conflicting
declarations, unsupported host links and stale JUnit cannot establish a pass.

Broad discovery expansion, compiler-resolved selection, large-monorepo coverage,
secure sandboxing and public multi-platform release delivery remain deferred.
The earlier live-agent and harness matrix above has not been repeated for this
milestone. Current full regression results are recorded below. Generated runtime
and pinned external-source measurements are described separately in
[the validation report](VALIDATION_INTELLIGENT.md), with their exact scope.


### Distribution and onboarding checks for this milestone

The source installer was executed with `GOCACHE=/tmp/radar-go-cache` and
`GOFLAGS=-buildvcs=false`, producing a runnable `radar 0.2.0` binary outside the
checkout. In a new committed temporary repository, its doctor command ran,
`setup --agent both --dry-run` changed no files or state, and two actual setup
runs preserved identical configuration/skill bytes. Both project MCP settings
and embedded recommendation/MCP-detail guidance were present. No home agent
configuration was changed.

Native Linux amd64 packaging completed, the archive checksum validated, and
the extracted executable ran `version` and a both-agent setup dry run. The
archive contained dependency inventory/licenses, project notices, agent guides
and the intelligent-verification example. `ldd` showed libc and its loader as
runtime libraries. Offline release-installer regression scenarios passed for
installation, preservation/replacement, invalid versions, checksum mismatch,
old glibc and a symlink archive member. Public release downloads and the manual
release-preparation workflow have not been exercised; no release was published.


### Final intelligent-verification regression results

Executed locally on 2026-10-09 for the frozen implementation:

- `go test -json ./...`: **228 passing test/subtest cases across 21 tested
  packages**, zero failures and skips.
- `go test -race ./...`, `go vet ./...` and the final CLI build: passed.
- The final embedded skill update additionally passed onboarding race tests.
- Existing organization, committed-verification and combined-integration demos:
  passed, including expected-failure assertions.

A (selected gate vs coverage), B (bounded change-aware test proposal and authorized
recommended execution) and C (reviewed exact candidate criterion evidence) are
complete for the documented supported scope. Recommendations include tested
explicit declared-contract impact with manifest locations; relevance remains
inferred. Passing a criterion does not resolve undeclared overall contract scope.

D discovery expansion is deferred: imported Python response models, richer
TypeScript client resolution and reviewed candidate acceptance were audited,
not implemented. E includes actual installer/onboarding/archive checks, the
generated Python HTTP/Node runtime demonstration and pinned Click read-only
selection evaluation. It does not establish production FastAPI execution,
compiled TypeScript behavior, independently labeled recommendation precision,
large-monorepo scalability or public multi-platform releases. The validation
report states measured examples and limitations; no cloud or release publishing
was performed.
