# Capabilities and limits

What Radar analyzes today, and where it stops. Anything outside this list is
reported as `unknown` or `incomplete`, never as a pass.

## Languages

| Language | Indexed | File dependencies resolved |
| --- | --- | --- |
| TypeScript / TSX | declarations, imports | relative paths (including ESM `.js` → `.ts` and `index` files), `tsconfig.json` `paths`/`baseUrl` with relative `extends`, repository packages by `package.json` name |
| JavaScript / JSX | declarations, imports | same as TypeScript, using `jsconfig.json` |
| Python | declarations, imports | absolute modules from the importer's directory, project root or `src/`; relative imports |
| Go | declarations, imports | packages inside any `go.mod` module in the repository |
| Rust | declarations, imports | `crate::`, `self::`, `super::` and `mod name;` |
| Java | declarations, imports | single-type, on-demand (`.*`) and static imports, by each file's declared `package` and file name |

Parsing is Tree-sitter, built into the binary. There is no type checking, call
resolution or runtime tracing. Import edges (`DEPENDS_ON`) are labeled
`inferred`: they follow each language's path conventions without a compiler.

Not handled: package-based tsconfig `extends`, project references,
`node_modules`, PYTHONPATH, Go vendoring, Cargo workspace metadata and dynamic
imports. Two repository packages with the same name are left unresolved.
Imports from outside the repository stay as unresolved `module` nodes.
TypeScript variance annotations (`interface X<in T>`) are not parsed by the
bundled grammar; those declarations are skipped with a warning.

Java types used from the same package need no import, so they get no edge;
test selection relates same-package tests instead. A type declared by two files
is left unresolved. Fully qualified names used without an import, reflection,
generated sources (annotation processors, Lombok) and Kotlin are not handled.

Working-tree indexing honors `.gitignore`. Symlinks and submodules in a
combined tree are refused.

## Contracts

Declared bindings in `.radar/contracts.json` point at a JSON or YAML OpenAPI
document or a JSON Schema. Radar compares the schema object at both revisions
and checks:

- removed properties
- type changes, including nullability (`nullable`, type arrays, `anyOf`/`oneOf`
  with `null`)
- required fields added or removed
- enum values added or removed
- validation constraints (`format`, lengths, ranges, `additionalProperties`)
- local `$ref`, recursive references and `allOf`

Compatibility depends on `direction`. Request schemas constrain what consumers
send, so widening is compatible. Response schemas constrain what they receive,
so narrowing is compatible. A lost required response field, a newly nullable
response field or a removed request enum value is breaking. Response enum
additions and request property removals are risks. Without a direction, every
change is a risk.

Not analyzed: several structured `oneOf` alternatives, `not`, conditionals,
tuple items and external `$ref`. If they stay the same they are ignored. If they
change, the binding is reported as unanalyzed and the report is `incomplete`.
Protobuf, runtime schema generation and network-call tracing are not supported.

Consumer `fields` are declarations. Radar does not prove the consumer reads
them at runtime.

## Contract discovery

`radar discover` proposes bindings from source without running anything:

| Pattern | Supported subset |
| --- | --- |
| Python models | Pydantic `BaseModel`, aliases, local and relative imports, one unambiguous base, nested models |
| Python endpoints | FastAPI and APIRouter decorators with literal paths and `response_model`, literal `include_router` prefixes |
| TypeScript types | interfaces, type aliases, re-exports and barrels |
| TypeScript consumers | typed `await fetch(...).json()` with a literal URL, `axios.get<T>()`, simple `axios.create()` instances, and member access on the result |
| Schema documents | literal JSON/YAML OpenAPI responses with local refs, JSON Schema properties |

A candidate needs a matching literal path and HTTP method. A matching type name
alone does not create one. Dynamic URLs, computed schemas, interceptors,
validators and serialization aliases are not resolved.

Candidates are warnings. They never fail a gate and are never written into the
manifest. To make one binding, review `proposed_manifest` and copy it into
`.radar/contracts.json`. For Python producers, commit an OpenAPI snapshot and
bind against it, because Radar does not import the application to generate one.

[`examples/fastapi-typescript`](../examples/fastapi-typescript/README.md) is
the reference fixture.

## Tests

Recognized result formats: `go test -json`, `python -m unittest`, `pytest`,
Jest, Vitest, `node --test`, `cargo test`, Maven Surefire totals, the JUnit
reports Maven and Gradle write for selected commands, and any JUnit XML report
declared with `junit`: one file, or a directory of `TEST-*.xml` files.
Inventory and selection are described in [TEST_SELECTION](TEST_SELECTION.md).

## Plans

Plans hold requirements, decisions, constraints, contract and graph deltas,
tasks with dependencies, acceptance criteria and assumptions. Verification
rules can check file existence, JSON/YAML properties, indexed entities, exact
schema objects and `test_run` evidence. A property-presence rule proves the
property is there and nothing more.

## Not supported

- compiler-backed semantics or SCIP ingestion
- incremental indexing
- Windows binaries (Git defaults such as `core.autocrlf` are not handled yet)
- release signing or notarization
- an OS sandbox for executed tests
- automatic dependency installation or `git fetch`
