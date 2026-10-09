# Roadmap

0. Foundation: CLI/configuration, SQLite transactions/migrations, graph, four language adapters, Git inspection, fixture and graph query. Gate: actual multilingual indexing/query test.
1. Architecture MVP: versioned plan/schema, context bundle, Codex skill, explicit deltas, DAG, preflight. Gate: deliberately inconsistent export plan is rejected with evidence and uncertainty.
2. Contract MVP: committed Git comparisons, explicit OpenAPI/JSON Schema consumer mapping, explainable changes and independent-change negatives. Gate: Python producer/TS consumer total → total_cents conflict detected without demo-specific logic.
3. Verification: committed implementation checks, digest-bound local review, opt-in test evidence, explicit contract drift and task feedback. Gate: compliant/noncompliant fixtures distinguish pass/fail/unknown.
4. Expansion (explicitly deferred beyond the first public MVP): compiler semantic resolution, SCIP ingestion, Protobuf, incremental parsing, scalable graph queries, richer language contracts, agent notifications and interactive UI.

Current acceptance evidence is maintained in IMPLEMENTATION_STATUS.md. Release readiness requires all relevant tests, limitations and installation checks; milestones are never inferred from document existence.
