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
cases and update the JSON schema, integration skill and CLI demos together.
For release packaging and dependency notices, see [releasing](docs/RELEASING.md).
