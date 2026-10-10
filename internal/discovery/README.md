# Bounded contract candidates

Discovery has separate syntax-tree resolvers for Python producers and TypeScript
consumers. The coordinator reads bounded Git/worktree source once, composes
registered endpoints with canonical local type relationships, and preserves
proposed/inferred/unknown findings separately from accepted JSON/OpenAPI contracts.
No imports, startup hooks, npm scripts or tests execute during discovery.

See [supported patterns and trust limits](../../docs/CAPABILITIES.md#contract-discovery),
[the representative project](../../examples/fastapi-typescript/README.md), and
`python_resolve_test.go`, `typescript_resolve_test.go`, `quality_test.go`.

`proposed_manifest` never persists automatically. Python source schemas require a
reviewed JSON/OpenAPI snapshot before declaring accepted obligations. Dynamic
schemas and URLs, ambiguous ownership, runtime validators/serialization and
middleware remain unresolved. New support must retain exact source provenance,
bounded traversal and negative regressions, rather than matching names lexically.
