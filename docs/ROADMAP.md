# Roadmap

0. Foundation: CLI/configuration, SQLite transactions/migrations, graph, four language adapters, Git inspection, fixture and graph query. Gate: actual multilingual indexing/query test.
1. Architecture MVP: versioned plan/schema, context bundle, Codex skill, explicit deltas, DAG, preflight. Gate: deliberately inconsistent export plan is rejected with evidence and uncertainty.
2. Contract MVP: committed Git comparisons, explicit OpenAPI/JSON Schema consumer mapping, explainable changes and independent-change negatives. Gate: Python producer/TS consumer total → total_cents conflict detected without demo-specific logic.
3. Verification: committed implementation checks, digest-bound local review, opt-in test evidence, explicit contract drift and task feedback. Gate: compliant/noncompliant fixtures distinguish pass/fail/unknown.
4. Agent and team workflow (0.2): location-independent IDs and worktree-shared state, inferred import dependencies and `affected`, robust contract comparison (annotations, nullability, enums, allOf, YAML), test environment errors and more harnesses (pytest, jest, vitest, JUnit), assumption lifecycle, next steps, human output, MCP server, Claude Code hook/skill and a GitHub Actions example. Gate: the regression scenarios in IMPLEMENTATION_STATUS.md.
5. Expansion (deferred): compiler semantic resolution, SCIP ingestion, tsconfig path aliases and workspace-aware resolution, Protobuf, incremental parsing, scalable graph queries, richer language contracts, agent notifications and interactive UI.

Current acceptance evidence is maintained in IMPLEMENTATION_STATUS.md. Release readiness requires all relevant tests, limitations and installation checks; milestones are never inferred from document existence.

## Agent-native milestone (implemented bounded vertical slices)

A: project setup and unified check; B: inspectable static discovery; C: private combined-branch preview and opt-in verification with the two-passing-branches demo; D: read-only MCP, bounded repair guidance and planning ownership/integration warnings; E: module path alignment, unpublished Linux artifact preparation and regression checks. See implementation status for actual current execution results.

Next: broaden imported FastAPI/TypeScript client resolution with scoped symbol evidence, use plan-declared integration suites to recommend checks, and benchmark representative monorepos. Compiler semantics, runtime transport resolution, prose contradiction detection, multi-platform releases, signing and incremental caching remain deferred.
