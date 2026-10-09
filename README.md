# Radar

**Plan against the real codebase. Coordinate changes across agents. Verify what actually shipped.**

Radar is an open-source, local-first architecture intelligence CLI for AI-assisted development. It connects structural source evidence, inferred import dependencies, explicit contracts, structured implementation plans and Git checkpoints. It runs without an LLM API key and works with Claude Code, Codex or any other coding agent (CLI, JSON output or MCP).

Current scope: structural indexing for TypeScript/JavaScript, Python, Go and Rust with file-level import dependencies; explicit JSON/YAML OpenAPI and JSON Schema contract comparisons; plan DAG/preflight checks with actionable next steps; checkpoint-bound verification with test evidence from common harnesses; impact analysis across files, contracts and plan tasks; an MCP server for agents. Compiler-resolved semantic analysis and runtime behavior tracing are not implemented. See [capabilities](docs/CAPABILITIES.md) and [implementation status](docs/IMPLEMENTATION_STATUS.md).

## Install from this checkout

Requires Go 1.23 or newer and a C compiler for the embedded Tree-sitter parsers. Dependencies download on the first build. No Python, Node or Rust toolchain is required for indexing.

```sh
go build -o bin/radar ./cmd/radar
# Or install in your Go binary directory:
go install ./cmd/radar
```

The module path is a project identifier; a public remote/release is not assumed to exist. Build and install locally with `bash scripts/install.sh /your/bin/directory`. Build a native-host distributable with `bash scripts/release.sh /tmp/radar-release`; this packages dependency notices and checksums. Remote publication and signing are not configured.

## Quickstart

```sh
bin/radar init --root examples/organization
bin/radar doctor --root examples/organization
bin/radar index --root examples/organization
bin/radar resolve ExportSummary --root examples/organization
bin/radar graph --root examples/organization --kind schema_field
bin/radar graph --root examples/organization --from file:backend/models.py --edge DEPENDS_ON --reverse
bin/radar contracts --root examples/organization
bin/radar plan "Implement asynchronous exports of organization-scoped datasets" \
  --root examples/organization
```

Commands print a readable summary; add `--json` for machine output. `radar help` lists commands and `radar help COMMAND` shows each command's flags (a command rejects flags that belong to other commands).

Entity IDs are readable and independent of where the repository is checked out, e.g. `file:backend/models.py`, `class:backend/models.py#ExportSummary`, `function:svc/server.go#Server.Handle`, `contract:contracts/openapi.json#/components/schemas/ExportSummary`. Plans reference them in task `components` and rules; `radar resolve QUERY` finds them.

`plan` writes a context bundle and an **incomplete** versioned plan under `.radar/plans/`. It does not generate an approved architecture. Use the [Claude Code](integrations/claude-code/README.md) or [Codex](integrations/codex/SKILL.md) integration, or your agent, to investigate code, compare alternatives, fill the design, and submit the plan for review. `--output path.json` chooses a new repository-relative artifact; existing artifacts are preserved.

```sh
radar preflight --plan path/to/plan.json            # findings + next_steps
radar tasks --plan path/to/plan.json                # DAG, parallel groups, task packets
radar affected --base main --head WORKTREE --plan path/to/plan.json
radar impact --base main --head producer
radar scan --base main --branches producer,consumer
radar verify --plan path/to/plan.json
radar explain FINDING_ID
```

For committed design and implementation checkpoints:

```sh
radar index --ref BASE_SHA
radar plan "feature intent" --ref BASE_SHA --output .radar/plans/design.json
radar preflight --plan .radar/plans/design.json --ref BASE_SHA
# Execute only after an actual local review; reviewer is a declaration, not authentication.
radar approve --plan .radar/plans/design.json --reviewer YOUR_NAME \
  --output .radar/plans/approved.json
radar test --plan .radar/plans/approved.json --ref IMPLEMENTATION_SHA \
  --allow-execution --timeout 30s -- python3 -m unittest test_export
radar verify --plan .radar/plans/approved.json --ref IMPLEMENTATION_SHA \
  --evidence EVIDENCE_ID
```

### Assumptions

Plan assumptions have a lifecycle: `{"id": "auth", "text": "...", "status": "open|accepted|resolved", "resolution": "..."}`. Open assumptions keep verification `unknown`. Mark one `resolved` (with the evidence) or `accepted` (with who accepts the risk and why) instead of deleting it. Changing an assumption changes the plan digest, so it needs a fresh review. Plain-string assumptions from older plans still load as open.

### Test evidence

A `test_run` acceptance rule declares the exact argument array before execution. Tests run in a private copy of the committed files, with a timeout and output bounds. They execute repository code with your operating-system permissions; the copy is **not a security sandbox**. Radar stores outcome and output digest, bound to repository, commit, plan digest, command and criterion; the last 4 KiB of output is shown to you but never stored.

- Recognized harnesses: `go test -json`, Python `-m unittest`, `pytest`, `jest`, `vitest`, `node --test`, `cargo test`, and any framework that writes a JUnit XML report declared with `"junit": "report.xml"`.
- Dependencies resolve offline from local caches (Go module/build cache, Cargo/rustup homes, npm cache, Python user base). Declare more with `"env": ["NAME"]` (pass-through variables), `"link": ["node_modules"]` (untracked dependency directories linked from the checkout) and `"setup": [["npm", "ci", "--offline"]]` (commands run first).
- `passed` requires executed, non-failing recognized tests. A zero-exit command with no recognized tests is `unknown`. A command that fails before any recognized test result (missing dependency, build or setup failure) is `error`, which is reported as an environment problem, not a test failure.

### Checkpoints and exit codes

All stateful commands require `init`. Linked Git worktrees without their own `.radar` share the main worktree's state, and evidence identifies the repository by its root commit, so parallel agents in separate worktrees can record evidence that verifies anywhere. `preflight` analyzes current source and rejects a plan whose indexed baseline has changed. `verify --ref SHA` analyzes a committed implementation; its approved baseline remains separate. Authoritative means the result is bound to a reviewed plan and committed source, not that every requirement passed. Working-tree indexing uses a content-derived observation ID, and working-tree verification is informational. `impact --head WORKTREE` produces warnings rather than authoritative integration failures. Branch analysis requires the selected Git repository top-level root. Radar never merges, rebases or pushes.

Exit codes: `0` successful command/informational report (which may contain warnings or unknown checks), `1` supported failed check/committed contract finding, `2` invocation/analysis error. Contract reports carry `status: incomplete` when a binding could not be analyzed; `--strict` turns that into exit `1`. Always inspect status and diagnostics; exit `0` does not mean every architectural requirement is verified.

## Explicit contract dependencies

Track `.radar/contracts.json` beside a JSON or YAML OpenAPI document, or a JSON Schema:

```json
{
  "version": 1,
  "bindings": [{
    "id": "organization-summary",
    "schema": "openapi.yaml",
    "pointer": "/components/schemas/OrganizationSummary",
    "producer": "backend/models.py",
    "consumer": "frontend/summary.ts",
    "direction": "response",
    "fields": ["total", "owner.id"]
  }]
}
```

Radar compares schema objects at committed revisions: removed properties, type changes (including nullability via `nullable`, type arrays or `anyOf` with `null`), required changes, enum values, and validation constraints, following local `$ref` and `allOf`. Annotations such as `format`, `description` or `x-*` never disable the comparison. Constructs Radar cannot reason about (several structured `oneOf` alternatives, `not`, external `$ref`) are compared locally: unchanged, they are ignored; changed, they are reported as unanalyzed and the report is `incomplete`. Consumers are identified by explicit declarations, including nested fields. `radar contracts` lints the manifest and flags declared fields that no longer appear in the consumer (a lexical check). This proves a conflict with the declared dependency; it does not prove that an arbitrary HTTP request uses that property at runtime. [Demo](docs/DEMO.md) documents executable scenarios and current limits.

## Agent and CI integrations

- **Claude Code**: `radar mcp` (MCP server), a skill and a Stop hook that hands failed plan checks back to the agent. See [integrations/claude-code](integrations/claude-code/README.md).
- **Codex**: [skill](integrations/codex/SKILL.md).
- **GitHub Actions**: an [example pull-request workflow](integrations/github-actions/README.md) annotates contract impact, affected files and conflicts with other open pull requests.

## Development

```sh
go test ./...
go vet ./...
go test -race ./...
make demo-all
```

Tests use deterministic source and real temporary Git repositories; no model outputs or external cloud service are needed. See [contributing](CONTRIBUTING.md), [architecture](docs/ARCHITECTURE.md), [roadmap](docs/ROADMAP.md), and [security](docs/SECURITY.md). MIT licensed, with dependency license notices preserved.

To inspect intent alongside code, use `radar graph --plan approved.json --format mermaid`; add `--projected` to apply explicit proposed graph deltas. Planning entities and relationships remain marked `proposed`, even after review.
