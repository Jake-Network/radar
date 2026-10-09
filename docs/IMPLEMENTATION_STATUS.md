# Implementation status

Verified locally on 2026-10-09 with Go 1.26.8, linux/amd64 (WSL workspace). Radar 0.1.0 implements the bounded first public MVP. This is local implementation and release preparation, not a claim that all runtime architecture is understood or that a public release has been published. No remote, tag, commit or push was created. The Radar directory belongs to a parent Git checkout; its existing staged README was preserved.

## Milestone acceptance

| Milestone | State | Executable acceptance evidence |
| --- | --- | --- |
| 0 Foundation | Complete for the initial release | Go CLI, configuration, transactional/versioned SQLite snapshots, unified graph, immutable Git inspection and actual four-language indexing/query |
| 1 Architecture MVP | Complete for the documented structural and explicit-contract scope | Grounded context, versioned plans, Codex skill, proposed graph overlay, contract projections, task DAG/packets and deliberately inconsistent architecture rejection |
| 2 Contract MVP | Complete for the supported JSON schema subset | Real Python producer / explicitly linked TypeScript consumer branches, supported directional compatibility checks, explanation, independent-change negatives and working-tree/checkpoint distinction |
| 3 Continuous verification | Complete for the declared deterministic rules | Reviewed plan digest, immutable baseline/implementation ancestry, approved-contract comparison, actual test evidence, stale-evidence rejection and task/agent feedback |
| 4 Expansion | Deferred beyond the first public MVP | Compiler semantics, SCIP, Protobuf, incremental parsing, large-repository optimization, additional integrations and interactive visualization |

Completion means the stated acceptance scenarios execute, not that every possible contract or architectural behavior is supported. Full semantic understanding, arbitrary HTTP consumer tracing and complete OpenAPI compatibility are not advertised.

## Executed checks

- `go test ./...`: **103 passed test/subtest cases across 13 tested packages**, zero failures or skipped test cases. The entry-point and value-model packages have no standalone tests (two package-level no-test events).
- `go vet ./...`: passed.
- `go test -race ./...`: passed across all tested packages.
- `gofmt -l cmd internal`: no unformatted files.
- `go mod tidy`: completed with pinned dependencies and checksums.
- `go build -o bin/radar ./cmd/radar`: passed.
- Both `examples/organization/demo.sh` and `examples/verification/demo.sh`: exited 0 against the current binary. Each asserts expected failure exits as well as successful behavior.
- Machine-readable schemas parse as JSON, shell scripts pass `bash -n`, and `git diff --check` passes.
- Local install helper produces a working CLI. Native Linux amd64 archive includes licenses, schemas, integration and clean examples; both bundled demos pass using the extracted executable. Two builds from identical inputs produce identical SHA-256 archives in this environment.

Coverage includes graph operations, historical/atomic snapshots and concurrent writers; all structural adapters, cancellation, parser errors and resource bounds; stable nested and Go receiver identities; symlink/boundary checks; strict plan/rule validation, DAG conflicts, duplicate contract projections, task packets and proposed graphs; directional schema compatibility and unsupported diagnostics; immutable Git comparisons, missing refs and repository-script suppression; committed approval checks, drift, task states, failed/zero-test/stale evidence and CLI workflows.

## End-to-end results

| Scenario | Verified result |
| --- | --- |
| A Multilingual indexing | Baseline fixture contains 47 entities and 51 relationships; TypeScript, JavaScript, Python, Go and Rust are represented, with persisted query and Mermaid export |
| B Cross-language incompatibility | `total` replaced by `total_cents` produces 4 committed producer/consumer-pair findings with explicit schema and consumer source evidence; producer and consumer source files do not overlap |
| C Architecture Preflight | Grounded export candidate retains unresolved assumptions; inconsistent candidate fails declared ownership and verification checks; task DAG and independently consumable instructions are produced |
| D Independent changes | Independent committed Python/TypeScript changes produce 0 contract findings and 0 diagnostics |
| E Checkpoints | Uncommitted contract edit produces 2 informational warnings; committed comparison can produce confirmed supported incompatibilities |
| F Implementation verification | Approved compliant commit passes all 3 checks and its task authoritatively; noncompliant commit fails contract projection, property criterion and actual unittest evidence; old-commit or changed-plan proof is rejected |

Latest local evidence: `/tmp/radar-final-organization.json`, `/tmp/radar-final-verification.json` and `/tmp/radar-mvp-tests.jsonl`. Temporary fixtures: `/tmp/radar-demo.v7SrPz` and `/tmp/radar-verification.JHfsWd`. These are execution records, not portable release artifacts; checked-in demos reproduce the scenarios.

## Architecture and boundaries

- Package boundaries separate CLI, source indexing, language parsing, immutable checkpoints, graph, explicit contracts, planning, verification, evidence and persistence. CLI and analysis modules are split by responsibility; the CLI entry point remains minimal.
- Tree-sitter provides structural syntax evidence. Doctor reports semantic adapters unavailable even if external compiler tools are installed. Import targets are unresolved module references; no verified CALLS graph is fabricated.
- Explicit JSON OpenAPI/JSON Schema bindings establish declared producer/consumer dependencies. Runtime access and FastAPI-generated schema parity remain unverified. Request/response direction matters; missing direction or ambiguous removals produce risks rather than confirmed violations.
- Compatibility covers a documented subset of property, type and required-field changes, local references and committed schema-file deletion. Unsupported schema constructs produce diagnostics, not compatibility guarantees.
- Plans distinguish proposed, inferred and verified evidence. Consequential review is a local digest-bound declaration, not authenticated approval and never permission for Git mutation.
- Authoritative verification requires a reviewed plan and committed implementation descended from its baseline. Unsupported rules, partial-index absence, vague contract projections and missing evidence remain unknown.
- Test execution requires explicit `--allow-execution`, runs declared commands against a private immutable snapshot and records bounded digest-based evidence. It is not an operating-system/network sandbox. Evidence checksums detect accidental alteration, not a malicious local user's forgery. Raw output is not persisted.
- Indexing replaces full snapshots and retains versions; efficient incremental parsing is deferred. Limits include 2 MiB per source file, 64 MiB aggregate source and 10,000 source files. Generated directories, symlinks and unsupported files are excluded with diagnostics where relevant.
- Native Linux amd64 is the tested distribution target. Windows/macOS native builds, libc portability, hosted CI and public distribution need their own execution evidence.
- No telemetry, unexpected source upload, mandatory model credential or automatic destructive Git operation is introduced.

## Next development milestone

Milestone 4 begins with optional TypeScript/Python semantic adapters and SCIP ingestion, preserving current structural support, explicit uncertainty and all A–F regression scenarios. Establish representative large-repository benchmarks before making performance claims or redesigning incremental storage.
