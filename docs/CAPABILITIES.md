# Supported capabilities

| Language | Structural indexing | File dependencies | Semantic indexing |
| --- | --- | --- | --- |
| TypeScript / TSX | Tree-sitter declarations and imports | Relative specifiers (`./x`, `../x`, ESM `.js` → `.ts`, `index` files) | Unavailable |
| JavaScript / JSX | Tree-sitter declarations and imports | Relative specifiers | Unavailable |
| Python | Tree-sitter declarations and imports | Absolute modules from the importer's directory, the repository root or `src/`; relative imports; `from pkg import submodule` | Unavailable |
| Go | Tree-sitter declarations and imports | Packages inside any `go.mod` module of the repository (non-test files) | Unavailable |
| Rust | Tree-sitter declarations and imports | `crate::`, `self::`, `super::` paths and `mod name;` declarations | Unavailable |

Entities carry repository, revision, source location, method, and evidence.
Entity IDs are readable and location independent (`kind:path#Qualified.Name`).
Syntactic declarations are static evidence. `DEPENDS_ON` file edges are
`inferred`: they follow each language's path conventions without a compiler,
tsconfig `paths` aliases, PYTHONPATH, Go vendoring or Cargo workspace metadata.
Package imports from outside the repository stay unresolved `module` nodes.
Indexing does not typecheck, establish runtime calls, resolve dynamic imports,
or prove authorization behavior. Syntax errors are diagnostic evidence.
`radar doctor` reports capabilities; these grammars are compiled into the binary.
Working-tree indexing honors `.gitignore` inside a Git work tree.

Contract analysis uses JSON or YAML OpenAPI documents and JSON Schema with
explicit producer/consumer bindings in `.radar/contracts.json`. Checked:
removed properties; type changes including nullability (`nullable`, type
arrays, `anyOf`/`oneOf` with `null`); required additions/removals; enum value
additions/removals; changed validation constraints (`format`, lengths, ranges,
`additionalProperties`, ...); local `$ref`, recursive references and `allOf`.
Annotations are ignored. Several structured alternatives, `not`,
conditionals, tuple items and external references are not analyzed: unchanged
they are ignored, changed they are reported as unanalyzed and the report
status is `incomplete`. It is not a complete OpenAPI compatibility
implementation. Protobuf, automatic FastAPI schema extraction and
network-call tracing are unavailable. Consumer field dependencies are
declarations, not verified runtime access; `radar contracts` flags declared
fields that do not appear in the consumer source (a lexical check).

Plans support requirements, decisions, constraints, contract and graph deltas,
tasks, DAG ordering, contract ownership conflicts, acceptance criteria and
assumptions with an open/accepted/resolved lifecycle. Preflight returns
`next_steps`. Without a reasoning agent, `radar plan` creates grounded context
(lexical matches plus their import neighbours) and labels design fields
incomplete. It does not invent an architecture.

Verification rules check file existence, JSON/YAML properties, indexed graph
entities, and explicit `test_run` command evidence. Approved contract deltas can
compare an exact schema object at a declared JSON pointer. Undeclared changes to
bound schemas are reported as drift. Passing a property-presence rule proves
presence only; it does not prove tenant isolation, job durability, scalability,
or execution behavior. Working-tree analysis is informational. Digest-bound
local review declarations and commit-pinned implementation checks support
authoritative reports. Stale test evidence cannot satisfy a different plan or
implementation commit. Unsupported checks remain unknown. A successful test
command is only evidence for its declared criterion; Radar does not inspect
test quality or guarantee runtime correctness.

Recognized test results: Go `test -json`, Python `-m unittest`, `pytest`,
`jest`, `vitest`, Node `--test`, Cargo `test`, and JUnit XML reports declared
with `junit`. A successful command with zero recognized executed tests yields
unknown; a command failing before any recognized result yields error;
timeout, output-limit or incomplete execution cannot become a pass.

`radar scan` reports a producer change against another branch only when that
branch changed the binding or its consumer file; branches that leave the
consumer untouched are covered by the producer branch's own finding. A
removed property is reported once (`field_removed`), not also as a lost
required guarantee.

`radar affected` follows inferred file dependencies backwards from changed
files (in both the base and head graphs) and maps them to contracts and plan
tasks. `radar mcp` exposes read-mostly commands as MCP tools; test execution and
review declarations are not exposed. `radar_index` returns counts and
diagnostics (`index --summary`) and tool output is capped at 40,000 bytes.

Native-host release packaging includes dependency notices and checksums;
published releases and signing are not configured. Initial execution evidence
covers linux/amd64. Windows/macOS builds and release binaries are not yet
validated. Indexing retains transactional full snapshots; incremental parsing
and source-specific semantic reference resolution are deferred.

Compatibility direction is explicit: request schemas constrain what consumers
send, response schemas what they receive. Request widening (integer→number,
added nullability, added enum values) and response narrowing are compatible.
Loss of a required response guarantee, a newly nullable response field or a
removed request enum value are breaking. Response enum additions and request
property removals are risks. Missing direction makes every change a risk.
Strict approved-design drift still reports unplanned changes even when
schema-compatible.
