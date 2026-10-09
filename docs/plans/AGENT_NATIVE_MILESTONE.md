# Agent-native milestone: inspected implementation plan

The baseline (2026-10-09) passed `go test ./...`, race tests, vet, CLI build and both existing demos. The initial unrelated `.serena/` directory is preserved.

Existing implementation supplies structural indexing, inferred import dependencies, SQLite snapshots, explicit contract manifests, grounded incomplete plans, task DAGs, reviewed commit-bound test evidence and agent integrations. It has no automatic producer/consumer discovery or combined branch source tree. The module path mismatches the requested repository. Existing verification aggregation also overlooks error/timeout outcomes, allowing misleading passed task status.

## Execution and acceptance

1. A: Add project-local idempotent setup (preserve settings, detect root, embed skill assets, dry run) and unified read-only check. Check must analyze without state or a manifest and report unknown coverage separately from failure. Retain 0/1/2 exit policy, with explicit require-complete.
2. B: Discover bounded static schema, FastAPI/Pydantic and typed TS consumer candidates. Keep candidate links inferred/proposed, include source evidence and unresolved ambiguity. Compare removed fields against supported consumers; never silently accept a proposed manifest.
3. C: Construct Git integration commits in a private repository with copied objects, no shared mutable object database or source index. Analyze the combined tree and optionally run explicit commands through existing evidence infrastructure. Bind outcomes to input SHAs, exact integration commit/tree, command and optional plan digest. Exercise passing branches whose combination fails, repairs, conflicts, environment errors and cleanup.
4. D: Improve deterministic context expansion and ownership/integration plan checks; expose safe read-only MCP tools and bounded actionable agent feedback. Fix error/timeout aggregation.
5. E: Align module path, prepare unpublished host release artifacts with notices/checksums, document exact supported/unsupported patterns, run full regression suite/demos and measure indexing before considering caching.

Independent implementation lanes may overlap in time; integration gates and documentation claims are accepted in the sequence above. No source history mutations, remote writes, automatic human approval or test execution from MCP are authorized.

## Trade-offs

Use existing Tree-sitter and schema comparison infrastructure instead of a new analysis framework. Conservative static discovery trades universal coverage for inspectable evidence. Git performs real merge semantics in a private object database; filesystem separation is not an OS sandbox. Initial distribution validates Linux amd64; other binary targets require evidence before support claims. Caching is deferred unless measurements justify invalidation complexity.
