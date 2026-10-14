# Contributing to Radar

Use Go 1.23 or newer and a C compiler (Tree-sitter uses CGO). Start with:

```sh
go test ./...
go vet ./...
go build ./cmd/radar
```

Keep changes small and add deterministic fixtures for new analysis. Include
negative tests: a supported detector must not report an unsupported inference
as a confirmed violation. Evidence categories and diagnostics are public
behavior. An unavailable analyzer is unknown, never a successful verification.

For a language adapter, document structural versus semantic capabilities,
source locations, unsupported syntax, bounds, and dependency licensing.
For contract checks, state exactly which schema semantics are checked and
test malformed input. Avoid fixture-specific detection logic.

Run `gofmt` on changed Go files. Describe the behavior, the evidence behind it,
and the tests in your pull request. Never add telemetry or source uploads by
default. Do not execute repository-provided programs during indexing.

Package boundaries are described in [architecture](docs/ARCHITECTURE.md). Keep
command parsing in `internal/cli`, test execution in `internal/evidence`, and
analysis rules in their owning modules. `cmd/radar` remains an entry point.
When changing evidence or plan semantics, add stale-commit and stale-plan negative
cases and update the JSON schema, integration skills (Claude Code and Codex),
MCP tool descriptions and CLI demos together. New commands go in the command
table in `internal/cli/commands.go` with their own flags and a human renderer.
Entity identities must stay independent of the checkout location.
For release packaging and dependency notices, see [releasing](docs/RELEASING.md).

## Add a detector

Own one tool or language at a time. Start with its existing package (for example
`internal/migrations`, `internal/discovery`, `internal/indexer` or
`internal/contracts`) rather than adding a second CLI analysis path. Detectors
consume committed files as data. They must not execute repository programs or
fetch. Preserve the owner package's existing `Result`/finding contract; there is
no universal detector plugin interface. Integration wires results into checks
and the built-in policy only when applicable.

Write a temporary-repository fixture with base, branch A, branch B, each branch
merged alone onto base, and the combined tree. Include a confirmed positive,
a benign negative, a base-existing issue, a single-branch issue and
malformed/unsupported input. Base-existing issues must not be blamed on a new
branch. Unsupported input is unknown, never PASS. Findings need repository
relative files, locations, supported evidence method and a useful repair hint.
Avoid fixture-name detection and aggregate counts that lose identity.

Run the owner package tests, `go test ./...`, `go vet ./...`, build and
`git diff --check`. Gate changes additionally need the smoke/demo checks; public
JSON/MCP or evidence changes need schema and agent-asset updates. Use
[the detector issue template](.github/ISSUE_TEMPLATE/detector.md) to propose a
bounded rule with positive and negative fixtures.
