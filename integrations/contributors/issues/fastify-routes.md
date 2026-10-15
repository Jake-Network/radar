# Discover literal Fastify shorthand routes

Local issue draft. Publication and `good first issue` assignment require a
maintainer to confirm the scope is small, still missing, and unclaimed.

## Engineering problem

A and B independently register the same literal GET path. Individual branches can look valid while the combined tree contains
the disputed identity or relationship. First check current support and attach a
minimal failing fixture before adding a detector.

## Scope and evidence

Owner: `internal/discovery`. TypeScript literal fastify.get/post path calls on a same-file Fastify instance.
Read committed files as data; no repository execution, network, or dependency.
Reuse the owner package result/finding contract and existing integration path.
Keep source paths repository-relative, retain source line locations, and state
whether each result is verified static or inferred/proposed. Migration adapters
use `migration_order`; declared schema changes use `no_breaking_contracts`;
discovery and import relationships remain proposals/inferences.

## Acceptance fixtures

- Positive: A and B independently register the same literal GET path.
- Benign/unsupported: Different methods are benign; register plugin prefixes, hooks and computed paths remain unknown. Proposed discovery cannot fail gate.
- Base-existing: put the same condition in base; do not newly attribute it to A/B.
- Single-branch: merge A alone and B alone, record any pre-existing defect
  separately; a confirmed integration regression must require their combination.
- Malformed: truncate the relevant input, duplicate identities/keys and exceed
  parser bounds. Preserve unknown diagnostics rather than reporting PASS.
- Portability: run equivalent fixtures from two different checkout paths and
  assert the same repository-relative finding identities.

Place temporary-repository tests in `internal/discovery` beside the existing adapter tests;
use descriptive tool/syntax fixtures, never a specific external project name.
Run `go test ./internal/discovery`, then the repository completion checks. If wiring a
required gate check changes, add text/JSON/MCP, schema and smoke coverage.

## Repair and limits

Review route ownership and add explicit bindings if enforcement is intended.
Do not automatically rewrite declarations or applied history. State unsupported
syntax and whether an existing adapter can cover the case before expanding scope.
