# Supported capabilities

| Language | Structural indexing | Semantic indexing |
| --- | --- | --- |
| TypeScript / TSX | Tree-sitter declarations and imports | Unavailable |
| JavaScript / JSX | Tree-sitter declarations and imports | Unavailable |
| Python | Tree-sitter declarations and imports | Unavailable |
| Go | Tree-sitter declarations and imports | Unavailable |
| Rust | Tree-sitter declarations and imports | Unavailable |

Entities carry repository, revision, source location, method, and evidence.
Syntactic declarations are static evidence; import targets may be unresolved.
Indexing does not typecheck, establish runtime calls, resolve arbitrary dynamic
imports, or prove authorization behavior. Syntax errors are diagnostic evidence.
`radar doctor` reports capabilities; these grammars are compiled into the binary.

Contract analysis uses JSON OpenAPI or JSON Schema documents and explicit
producer/consumer bindings in `.radar/contracts.json`. Its initial subset checks
removed properties, changed types, and newly required request properties (response additions are not treated as breaks; unspecified direction is a warning), with local
`$ref` resolution. It is not a complete OpenAPI compatibility implementation;
composition and external references produce diagnostics. YAML, protobuf,
automatic FastAPI schema extraction, and network-call tracing are unavailable.
Consumer field dependencies are declarations, not verified runtime access.

Plans support requirements, decisions, constraints, contract and graph deltas,
tasks, DAG ordering, contract ownership conflicts, and acceptance criteria.
Without a reasoning agent, `radar plan` creates grounded context and labels
design fields incomplete. It does not invent an architecture.

Verification rules check file existence, JSON properties, indexed graph entities,
and explicit `test_run` command evidence. Approved contract deltas can compare an
exact schema object at a declared JSON pointer. Undeclared changes to bound schemas
are reported as drift. Passing a property-presence rule proves presence only; it does
not prove tenant isolation, job durability, scalability, or execution behavior.
Working-tree analysis is informational. Digest-bound local review declarations and
commit-pinned implementation checks support authoritative reports. Stale test
evidence cannot satisfy a different plan or implementation commit. Unsupported checks
remain unknown. A successful test command is only evidence for its declared criterion;
Radar does not inspect test quality or guarantee runtime correctness. Supported
execution result parsers are Go `test -json`, Python `-m unittest`, Node `--test`
and Cargo `test`. A successful command with zero recognized executed tests yields
unknown, and timeout/output-limit/incomplete execution cannot become a pass.

Native-host release packaging includes dependency notices and checksums; published
releases and signing are not configured. Initial execution evidence covers linux/amd64. Windows/macOS builds and release binaries are not yet validated. Indexing retains transactional full snapshots; efficient incremental parsing and source-specific semantic reference resolution are deferred.

Compatibility direction is explicit: request integer-to-number widening and response number-to-integer narrowing do not produce incompatibility errors. Loss of a required response guarantee is checked; request property removal or missing direction is a risk rather than a confirmed runtime break. Strict approved-design drift still reports unplanned changes even when schema-compatible.
