# Agent-native development milestone report

Implemented locally on 2026-10-09. No commit, push, remote branch mutation,
release publication or hosted workflow execution was performed. Existing
unrelated `.serena/` state was preserved. CLI version remains 0.2.0; a release
version/tag decision is deferred to the maintainer.

## Implemented product behavior

- `setup --agent codex|claude|both` detects repository roots (including unborn
  and nested checkouts), initializes local configuration, installs embedded
  project skills and merges MCP configuration. Dry run, idempotency, unrelated
  setting preservation, conflicting configuration refusal and optional bounded
  Claude hook are tested. Ambiguous quoted TOML requires manual reconciliation.
- `check --base REF` works without initialization or accepted contracts. It
  combines changed files, reverse import dependencies, declared contract impact,
  manifest lint, discovered candidates and optional plan criteria. JSON contains
  per-check coverage, findings, remediation, feedback digest and a suggested
  two-attempt repair budget. Refs are pinned and indexed source fingerprints
  checked at analysis boundaries. Warnings remain informational; explicit
  `--require-complete` rejects incomplete coverage.
- `discover` finds bounded static OpenAPI/JSON Schema, same-file
  FastAPI/Pydantic and imported typed TypeScript literal-fetch relationships.
  Candidates have locations, field access evidence and ambiguity. Proposed
  manifests are returned for review, never accepted automatically. Removed
  response fields still read by supported consumers produce warning findings.
- `merge-check` performs actual Git merges in private copied object state,
  reports textual conflicts, analyzes combined contracts/dependencies and
  optional plan rules, and executes only explicitly authorized commands.
  Default preview/verification writes no files into the original checkout.
  `--evidence-output .radar/evidence/new.json` explicitly persists new metadata.
  Evidence binds candidate commit/tree, ordered input SHAs, command and optional
  plan digest. Original branch evidence is never reused. Tracked commits,
  tracked edits and new untracked source introduced by a command prevent an
  original-candidate passing claim.
- MCP adds read-only check, discovery and merge preview tools. Execution and
  human approval arguments are rejected. Agent skills explain grounded design,
  combined verification and bounded repair; the Claude hook emits JSON feedback.
- Planning expands evidence context through imports and explicit contract edges,
  warns about unordered shared component ownership and absent shared integration
  criteria, and supplies repair/reverification guidance. Existing consequential
  alternatives/trade-offs, assumptions, DAG and criterion validation are kept.
- Verification preserves environment-error/timeout outcomes instead of letting
  them appear as completed tasks. Legacy plans, explicit manifests, SQLite state
  and individual test evidence remain readable. Combined evidence is separate.

## Architecture decisions and trade-offs

The modular Go architecture and existing Tree-sitter, schema, graph, planning
and evidence services are retained. No new Go dependency, agent runtime or LLM
service was introduced. Conservative discovery favors inspectable candidates
over unsupported runtime conclusions. Real Git supplies merge semantics;
private objects avoid shared writable Git state. Hooks, Git protocol use and
lazy fetching are disabled for inspection/preview. Filesystem separation is
not an OS sandbox: opt-in code execution retains host privileges.

Recognized test cases, harness counts and output digests are reused from the
existing execution layer. Raw combined test output is omitted; bounded unittest
case identifiers and repository source locations give repair evidence. A zero
exit without recognized tests is unknown; missing tools/dependencies and
startup/build errors are environment errors. Generic combined execution does
not fabricate reviewed-plan criterion evidence. A shared plan test criterion
also cannot prove semantic test coverage.

## Main changed modules and commands

| Area | Files / modules |
| --- | --- |
| Onboarding | `internal/onboarding/`, `internal/cli/setup.go`, setup tests and embedded assets |
| Unified analysis | `internal/cli/check.go`, `affected.go`, CLI aggregation tests |
| Discovery | `internal/discovery/`, `internal/cli/discovery.go`, FastAPI/TS fixtures |
| Combined verification | `internal/integration/`, `internal/evidence/candidate.go`, `internal/cli/merge_check.go`, `schemas/integration-evidence.schema.json` |
| Planning and feedback | `internal/planning/context.go`, `preflight.go`, `internal/verification/`, `internal/cli/mcp.go`, both agent skills/hooks |
| Distribution | `go.mod` and matching imports, release/installer scripts, CI and manual release workflow |
| Demonstration and documentation | `examples/integration/`, README, capabilities, architecture, roadmap, status, agent guides and supporting reports |

New commands: `setup`, `check`, `discover`, `merge-check`.
New MCP tools: `radar_check`, `radar_contracts_discover`, `radar_merge_check`.
Existing command flags and state formats are preserved. `verify` now returns
exit 2 for an observed execution/environment error or timeout. The legacy
`test` command retains its informational exit behavior for stored environment
error records; consumers must inspect its status. Normal warnings do not fail
CI. No new tool can record human approval.

## Executed validation

Environment: Go 1.26.8, Linux amd64, WSL workspace. Writable `/tmp` Go build cache
was used where the default cache was read-only.

| Check | Actual result |
| --- | --- |
| `go test ./...` (JSON event capture) | Exit 0; 176 passed test/subtest events across 19 tested packages; no failing or skipped test cases; entry-point package has no tests |
| `go test -race ./...` | Exit 0 across all packages |
| `go vet ./...` | Exit 0 |
| `go build ./cmd/radar` | Exit 0; nonfatal read-only module stat-cache warning |
| `gofmt -l cmd internal`, `git diff --check` | Empty / exit 0 |
| Existing organization and verification demos | Both exit 0, including expected-failure assertions |
| New combined integration demo | Exit 0; individual pass, textual merge pass, combined test failure, repaired pass |
| Offline binary installer scenarios | Exit 0; install, overwrite protection, explicit replacement, invalid version, bad checksum, old glibc and unsafe archive rejection |
| Native release packaging | Exit 0; Linux amd64 archive with notices and checksum |
| Packaged executable and central demo | Executable runs, libc resolves, central demo exit 0, checksum validates |
| New evidence JSON schema | A real combined execution report validates against draft 2020-12 schema |

Additional tests cover malformed/quoted TOML, symlinks, missing tools and
dependencies, no-test commands, timeouts, conflict cleanup, removed response
fields, unrelated schema changes, ambiguous candidates, lexical false
positives, static plan failures and preservation of a dirty source index/tree.

Local evidence files: `/tmp/radar-final-tests.jsonl`,
`/tmp/radar-final-race.log`, `/tmp/radar-final-{organization,verification,integration}.log`,
`/tmp/radar-final-packaged-integration.log`. A representative JSON report is
`/tmp/radar-final-artifact-igcbvzsr/report.json`. These paths are local run
artifacts, not portable distribution requirements.

## Demonstrated failure beyond Git conflicts

The [reproducible script](../examples/integration/demo.sh) has a Python product
response and TypeScript checkout quantity. One agent branch doubles price,
another doubles quantity. Both independently pass the same budget test; their
combination merges cleanly and fails `test_combined_budget`. Radar reports the
failed case and `test_checkout.py:10`, then verifies a corrected combination.
The invariant is in the fixture test, not hardcoded in Radar. Separate tests
exercise declared breaking API fields with affected consumers and unrelated
schema changes. The demo reads TS configuration from Python; it does not run a
browser or HTTP server.

## Performance and distribution evidence

Nine full-index runs measured 0.36–0.38 seconds for the Radar source copy,
0.20–0.22 seconds for 1,000 small TypeScript files, and 0.01–0.02 seconds for the
multilingual fixture. Peak RSS was approximately 16–55 MiB. See
[measurements and limits](PERFORMANCE.md). These warm-cache `/tmp` fixtures do
not prove large-monorepo scalability or discovery throughput; caching and
storage redesign were deliberately deferred.

The Go module now matches `github.com/Jake-Network/radar`. A no-Go installer and
manual artifact workflow prepare Linux amd64 distribution (workflow target
Ubuntu 22.04 / glibc 2.35+). Local archive:
`/tmp/radar-release-milestone-e/radar-linux_amd64.tar.gz`.
Public downloads, hosted CI, signing and other native targets were not tested
or published. Source installation requires Go and a C compiler.

## Remaining limits and recommended next milestone

Static discovery does not resolve imported Python response models, complex TS
clients, aliases/re-exports, variable scope shadowing, runtime serialization or
service origins. It cannot automatically make discovered links authoritative.
Compiler semantics, prose contradiction detection and exhaustive architecture
verification remain unsupported. Symlink/submodule preview is rejected.
Execution observes the supplied command; automatic suite selection, dependency
installation and plan-evidence promotion are deferred. Generated/cache paths
and transient changes reverted during execution are not runtime integrity
proof. The repair budget is guidance plus a bounded Stop hook, not an autonomous
retry controller. Live Codex/Claude sessions and hosted workflows were not
retested for this milestone.

Next product milestone: broaden scoped FastAPI/TypeScript relationship
resolution and recommend plan-declared integration suites. Preserve explicit
execution authorization, measure realistic monorepos, and validate hosted
agent/distribution flows before adding incremental storage or more binary
platforms.
