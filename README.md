# Radar

**Plan against the real codebase. Coordinate changes across agents. Verify what actually shipped.**

Radar is an open-source, local-first architecture intelligence CLI for AI-assisted development. It connects structural source evidence, explicit contracts, structured implementation plans and Git checkpoints. It runs without an LLM API key and works with Codex or another coding agent.

Initial public MVP: genuine structural indexing for TypeScript/JavaScript, Python, Go and Rust; explicit JSON contract comparisons; plan DAG/preflight checks; checkpoint-bound verification, explicit test evidence and agent task feedback. Compiler-resolved semantic analysis and automatic runtime behavior tracing are not implemented. See [capabilities](docs/CAPABILITIES.md) and [implementation status](docs/IMPLEMENTATION_STATUS.md).

## Install from this checkout

Requires Go 1.23 or newer and a C compiler for the embedded Tree-sitter parsers. Dependencies download on the first build. No Python, Node or Rust compiler is required for structural indexing.

```sh
go build -o bin/radar ./cmd/radar
# Or install in your Go binary directory:
go install ./cmd/radar
```

The module path is a project identifier; a public remote/release is not assumed to exist. Build and install locally with `bash scripts/install.sh /your/bin/directory`. Build a native-host distributable with `bash scripts/release.sh /tmp/radar-release`; this packages dependency notices and checksums. Remote publication and signing are not configured.

## Quickstart

```sh
bin/radar init --root examples/organization
bin/radar doctor --root examples/organization --json
bin/radar index --root examples/organization
bin/radar graph --root examples/organization --kind function --json
bin/radar graph --root examples/organization --kind schema_field --json
bin/radar graph --root examples/organization --format mermaid
bin/radar plan "Implement asynchronous exports of organization-scoped datasets" \
  --root examples/organization --json
```

`plan` writes a context bundle and an **incomplete** versioned plan under `.radar/plans/`. It does not generate an approved architecture. Use the [Codex integration](integrations/codex/SKILL.md) or your agent to investigate code, compare alternatives, fill the design, and submit the plan for review. `--output path.json` chooses a new repository-relative artifact; existing artifacts are preserved.

```sh
bin/radar preflight --root YOUR_REPOSITORY --plan path/to/plan.json --json
bin/radar tasks --root YOUR_REPOSITORY --plan path/to/plan.json --json
bin/radar verify --root YOUR_REPOSITORY --plan path/to/plan.json --json
bin/radar impact --root YOUR_REPOSITORY --base main --head producer --json
bin/radar scan --root YOUR_REPOSITORY --base main --branches producer,consumer --json
bin/radar explain FINDING_ID --root YOUR_REPOSITORY --json
```

For committed design and implementation checkpoints:

```sh
radar index --ref BASE_SHA --json
radar plan "feature intent" --ref BASE_SHA --output .radar/plans/design.json
radar preflight --plan .radar/plans/design.json --ref BASE_SHA --json
# Execute only after an actual local review; reviewer is a declaration, not authentication.
radar approve --plan .radar/plans/design.json --reviewer YOUR_NAME \
  --output .radar/plans/approved.json --json
radar test --plan .radar/plans/approved.json --ref IMPLEMENTATION_SHA \
  --allow-execution --timeout 30s --json -- python3 -m unittest test_export
radar verify --plan .radar/plans/approved.json --ref IMPLEMENTATION_SHA \
  --evidence EVIDENCE_ID --json
```

A `test_run` acceptance rule must declare the exact argument array before execution. Tests run in a private copy of the committed files, with a timeout and output bounds. They execute repository code with your operating-system permissions; the copy is **not a security sandbox**. Radar stores outcome and output digest, bound to repository, commit, plan digest, command and criterion. Missing or stale evidence remains unknown. Passing evidence requires observed executed tests from a supported harness: `go test -json`, Python `-m unittest`, Node `--test`, or `cargo test`; a zero-exit command with no recognized tests remains unknown.

All stateful commands require `init`. `preflight` analyzes current source and rejects a plan whose indexed baseline has changed. `verify --ref SHA` analyzes a committed implementation; its approved baseline remains separate. Authoritative means the result is bound to a reviewed plan and committed source, not that every requirement passed. `verify` without `--ref` analyzes current source again. Working-tree indexing uses a content-derived observation ID, and working-tree verification is informational. `impact --head WORKTREE` produces warnings rather than authoritative integration failures. Branch analysis requires the selected Git repository top-level root. Radar never merges, rebases or pushes. Repository test execution requires the explicit `test --allow-execution` command.

JSON reports preserve evidence levels and unknown states. Exit codes: `0` successful command/informational report (which may contain warnings or unknown checks), `1` supported failed check/committed contract finding, `2` invocation/analysis error. Always inspect status and diagnostics; exit `0` does not mean every architectural requirement is verified.

## Explicit contract dependencies

Track `.radar/contracts.json` beside a JSON OpenAPI or JSON Schema document:

```json
{
  "version": 1,
  "bindings": [{
    "id": "organization-summary",
    "schema": "openapi.json",
    "pointer": "/components/schemas/OrganizationSummary",
    "producer": "backend/models.py",
    "consumer": "frontend/summary.ts",
    "direction": "response",
    "fields": ["total"]
  }]
}
```

Radar compares supported schema properties at committed revisions and identifies consumers using these explicit dependency declarations. Removing `total` while a consumer branch retains its declaration produces a reproducible finding with producer/consumer provenance. This proves a conflict with the declared dependency; it does not prove that an arbitrary HTTP request uses that property at runtime. Unsupported schema constructs produce diagnostics. [Demo](docs/DEMO.md) documents the executable scenarios and current limits.

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
make demo
bash examples/organization/demo.sh "$(pwd)/bin/radar"
bash examples/verification/demo.sh "$(pwd)/bin/radar"
```

Tests use deterministic source and real temporary Git repositories; no model outputs or external cloud service are needed. See [contributing](CONTRIBUTING.md), [architecture](docs/ARCHITECTURE.md), [roadmap](docs/ROADMAP.md), and [security](docs/SECURITY.md). MIT licensed, with dependency license notices preserved.

To inspect intent alongside code, use `radar graph --plan approved.json --format mermaid`; add `--projected` to apply explicit proposed graph deltas. Planning entities and relationships remain marked `proposed`, even after review. Components and consumer references in plans use graph entity IDs, available from `index`/`graph` output.
