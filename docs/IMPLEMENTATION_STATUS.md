# Implementation status

Verified locally on 2026-10-09 with Go 1.26.8, linux/amd64 (WSL workspace). Radar 0.2.0 extends the bounded first public MVP with the agent and team workflow milestone. This is local implementation and release preparation, not a claim that all runtime architecture is understood or that a public release has been published. No remote, tag, commit or push was created.

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
- Native Linux amd64 is the tested distribution target. Windows/macOS native builds, hosted CI runs and public distribution need their own execution evidence. `.github/workflows/ci.yml` has run under `act`, not yet on a hosted GitHub runner.

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
Python response models, broad TS client patterns, automated suite recommendation,
semantic contradiction detection in architecture prose, large-monorepo evidence,
multi-platform published binaries and signing. Existing plan/manifest/evidence
formats remain readable; combined evidence is a separate metadata format.

Next: expand scoped producer/consumer resolution and recommend plan-declared
integration suites while preserving explicit execution authorization. See
[performance measurements](PERFORMANCE.md) before planning cache redesign.
