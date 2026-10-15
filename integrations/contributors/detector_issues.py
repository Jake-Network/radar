#!/usr/bin/env python3
"""Render scoped detector issue drafts locally; never contacts GitHub."""
import argparse
import pathlib

# Adapter tasks deliberately stop at the existing owning package contract.
DETECTORS = [
    ('liquibase-xml', 'Resolve literal Liquibase logicalFilePath changeset identities', 'internal/migrations',
     'Literal <changeSet id="..." author="..."> entries in a declared XML changelog; use (logicalFilePath or repository-relative file, id, author).',
     'A and B add different XML files with the same literal logicalFilePath/id/author identity; current cross-file scope is unknown.',
     'Same id with distinct authors is benign; include/import paths outside the repository, properties and SQL-format changelogs are unknown.',
     'Renumber the new changeset identity; preserve the base history.'),
    ('prisma-migrations', 'Detect modifications to already committed Prisma migrations', 'internal/migrations',
     'Compare base and candidate bytes for existing prisma/migrations/<timestamp>_<name>/migration.sql paths; preserve the existing full-directory identity rule.',
     'A changes an existing migration.sql and B adds a later migration; report the exact base-file mutation separately from integration-only failures.',
     'Adding a distinct new migration is benign; a rename/delete or unsupported directory layout is unknown; do not claim SQL compatibility or require every finding to be integration-only.',
     'Regenerate one new migration against the intended migration chain.'),
    ('spring-routes', 'Discover finite literal Spring MVC path arrays', 'internal/discovery',
     'Extend existing Java Spring route discovery to @GetMapping({"/orders","/purchases"}) literal path arrays, bounded to a documented item limit.',
     'A adds a literal /orders alias in a path array and B introduces a second GET /orders handler; preserve all source locations.',
     'GET and POST on one path are distinct; constant expressions, property expansion and inherited mappings are unknown. Discovery is proposed evidence, not an automatic hard failure.',
     'Review and declare the endpoint ownership before enabling a failing contract.'),
    ('express-routes', 'Discover literal Express router mounts', 'internal/discovery',
     'TypeScript express.Router() values, literal router.get/post paths and literal app.use prefix within one file.',
     'A adds router.get("/orders") and B mounts another identical GET /api/orders handler.',
     'Distinct methods are benign; dynamic prefixes and cross-file router aliases are unknown. Emit proposals only.',
     'Review discovered endpoints and declare a contract for owned routes.'),
    ('kotlin-imports', 'Resolve Kotlin same-project literal imports', 'internal/indexer',
     'Kotlin package declarations and fully qualified non-star import paths within committed Gradle source roots.',
     'A renames a package while B adds an import of its old package; both must resolve to the same baseline file where supported.',
     'Unrelated package names must not connect; star imports, type aliases and generated source sets remain unresolved/inferred.',
     'Update imports on the consuming branch; never claim compiler-level resolution.'),
    ('flyway-repeatable', 'Track Flyway repeatable migration identities', 'internal/migrations',
     'Configured Flyway SQL directory with R__<description>.sql; normalize identity according to the existing Flyway adapter.',
     'A and B add conflicting definitions of the same new repeatable migration identity.',
     'Distinct descriptions are benign; placeholders and callbacks are unsupported; base-existing repeats must not be blamed on branches.',
     'Consolidate the repeated view/procedure definition before landing.'),
    ('dbmate-migrations', 'Detect dbmate duplicate migration versions', 'internal/migrations',
     'db/migrations/<numeric-version>_<description>.sql with literal -- migrate:up and -- migrate:down separators.',
     'A and B independently add different migration files sharing one numeric version.',
     'Different numeric versions are benign; malformed separators and nonnumeric versions yield unknown.',
     'Renumber one new migration while keeping the base migration unchanged.'),
    ('goose-migrations', 'Detect goose SQL migration version collisions', 'internal/migrations',
     'Configured migrations directory with <numeric-version>_<name>.sql and -- +goose Up marker.',
     'A and B add different SQL files with the same numeric migration version.',
     'Different versions are benign; Go-based executable migrations and malformed annotations remain unknown.',
     'Allocate a distinct version to one new migration.'),
    ('drizzle-journal', 'Detect divergent Drizzle journal entries', 'internal/migrations',
     'Literal drizzle/meta/_journal.json entries idx, tag and when with corresponding committed SQL files.',
     'A and B append different tags for the same journal idx relative to base.',
     'Identical entry bytes are benign; unsupported journal version, duplicate fields and missing SQL are unknown.',
     'Regenerate the later branch journal against the intended predecessor.'),
    ('rails-migrations', 'Detect Rails migration timestamp collisions', 'internal/migrations',
     'db/migrate/<14-digit-timestamp>_<name>.rb; use filename identity, without running Ruby.',
     'A and B add separate migrations sharing the same timestamp version.',
     'Distinct timestamps are benign; executable path configuration and nonstandard migration filenames are unknown.',
     'Give one new migration a unique timestamp; preserve applied migrations.'),
    ('fastify-routes', 'Discover literal Fastify shorthand routes', 'internal/discovery',
     'TypeScript literal fastify.get/post path calls on a same-file Fastify instance.',
     'A and B independently register the same literal GET path.',
     'Different methods are benign; register plugin prefixes, hooks and computed paths remain unknown. Proposed discovery cannot fail gate.',
     'Review route ownership and add explicit bindings if enforcement is intended.'),
    ('ktor-routes', 'Discover bounded Kotlin Ktor routes', 'internal/discovery',
     'Literal routing { route("/prefix") { get("/path") { ... } } } nesting with bounded depth.',
     'A and B add the same GET /prefix/path route inside supported literal nesting.',
     'Different methods are benign; extension builders, dynamic path variables and conditional registration are unknown. Proposals only.',
     'Review declarations and assign endpoint ownership.'),
    ('sqlalchemy-table-keys', 'Discover literal SQLAlchemy table identities', 'internal/discovery',
     'Python declarative classes with literal __tablename__ and literal __table_args__ schema dictionary.',
     'A and B add different classes that declare the same schema/table identity.',
     'Same table name in different schemas is benign; inheritance, dynamic names and extend_existing are unsupported. Report proposals until explicitly declared.',
     'Review model ownership and declare the relevant unique-key invariant.'),
    ('graphql-union-members', 'Extend bounded GraphQL union member obligations', 'internal/contracts',
     'Declared GraphQL response binding with one literal union declaration whose member object definitions occur in the same file; keep unsupported directives/interfaces unknown.',
     'A removes an object member from a declared union that B consumes; candidate must retain base consumer obligations.',
     'Adding a union member and unrelated type changes are benign; external members, recursive expansions and directives/extensions remain unknown.',
     'Preserve the consumed member or record an explicit reviewed consumer retirement.'),
    ('protobuf-oneof', 'Extend Protobuf contract oneof membership checks', 'internal/contracts',
     'Declared proto3 message binding with literal oneof field tags and consumer field paths; stay within current schema parser.',
     'A changes a consumed field oneof membership while B relies on the original membership.',
     'Independent added tags are benign; unsupported proto editions, custom options and groups yield unknown.',
     'Preserve supported consumer semantics or migrate the declared consumer before retirement.'),
]


def render(entry):
    slug, title, owner, scope, positive, negative, repair = entry
    return f'''# {title}

Local issue draft. Publication and `good first issue` assignment require a
maintainer to confirm the scope is small, still missing, and unclaimed.

## Engineering problem

{positive} Individual branches can look valid while the combined tree contains
the disputed identity or relationship. First check current support and attach a
minimal failing fixture before adding a detector.

## Scope and evidence

Owner: `{owner}`. {scope}
Read committed files as data; no repository execution, network, or dependency.
Reuse the owner package result/finding contract and existing integration path.
Keep source paths repository-relative, retain source line locations, and state
whether each result is verified static or inferred/proposed. Migration adapters
use `migration_order`; declared schema changes use `no_breaking_contracts`;
discovery and import relationships remain proposals/inferences.

## Acceptance fixtures

- Positive: {positive}
- Benign/unsupported: {negative}
- Base-existing: put the same condition in base; do not newly attribute it to A/B.
- Single-branch: merge A alone and B alone, record any pre-existing defect
  separately; a confirmed integration regression must require their combination.
- Malformed: truncate the relevant input, duplicate identities/keys and exceed
  parser bounds. Preserve unknown diagnostics rather than reporting PASS.
- Portability: run equivalent fixtures from two different checkout paths and
  assert the same repository-relative finding identities.

Place temporary-repository tests in `{owner}` beside the existing adapter tests;
use descriptive tool/syntax fixtures, never a specific external project name.
Run `go test ./{owner}`, then the repository completion checks. If wiring a
required gate check changes, add text/JSON/MCP, schema and smoke coverage.

## Repair and limits

{repair}
Do not automatically rewrite declarations or applied history. State unsupported
syntax and whether an existing adapter can cover the case before expanding scope.
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', required=True, type=pathlib.Path)
    args = parser.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    paths = [(args.out / (entry[0] + '.md'), render(entry)) for entry in DETECTORS]
    if any(path.exists() for path, _ in paths):
        parser.error('output contains an existing issue draft; choose an empty directory')
    for path, content in paths:
        path.write_text(content)
    print(f'{len(paths)} local issue drafts written to {args.out}')


if __name__ == '__main__':
    main()
