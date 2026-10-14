# Detector contribution backlog (#15)

Local drafts, not fifteen published issues. Recheck current code before selecting
an item: another contributor may already implement it. External first-PR merges
and the 48-hour review target require real contributors and remain unverified.
Each row is one possible narrow follow-up, contingent on its stated syntax
being unsupported. Use the detector template and the fixtures in the owner
package. Start with `go test ./internal/<owner>`; follow CONTRIBUTING's full
checks before a PR. Do not treat an unsupported detector as passing.

| Draft | Owner / fixture location | Bounded rule and negative case |
| --- | --- | --- |
| Alembic revision diagnostics | migrations | Duplicate literal revision IDs; distinct IDs benign |
| Alembic depends_on | migrations | Literal dependency edges; computed expression unknown |
| Django swappable dependency | migrations | Literal swappable target; dynamic settings unknown |
| Prisma history | migrations | Literal timestamps and backdating warning; distinct ordered benign |
| Liquibase XML | migrations | Duplicate literal id/author; different author benign |
| Liquibase YAML | migrations | Same identity rule; malformed YAML unknown |
| Diesel migration directories | migrations | Directory version identity; up/down count once |
| sqlx migration suffixes | migrations | Version identity; reversible pair benign |
| Express literal routes | discovery | Method/path in same app; dynamic prefix unknown |
| Spring literal routes | discovery | Class and method literal prefix; variable annotation unknown |
| Gin literal routes | discovery | Literal router group prefix; distinct groups benign |
| axum literal routes | discovery | Literal method/path; dynamic router composition unknown |
| Python re-export resolution | indexer | Explicit __all__ names; calculated exports unknown |
| Java static import resolution | indexer | Literal named static import; wildcard unresolved |
| pnpm workspace importer edge cases | lockfiles | Direct dependency existence; aliases/protocols unknown |
| Cargo renamed dependency diagnostics | lockfiles | Literal package alias; unresolved target unknown |

Do not create duplicate issues for support already implemented. A maintainer
should refine each accepted row into a concrete test, then choose labels and an
actual reviewer before publishing. Fifteen drafts are a preparation artifact,
not satisfaction of the roadmap's external-community acceptance criterion.
