# Inspectable contract discovery

Run `radar discover --json` without initializing state or writing a manifest.
The report contains schemas, proposed producer/consumer candidates, source
locations, field accesses, ambiguities and an inspectable `proposed_manifest`.
Discovery never modifies accepted bindings. Review a candidate and copy usable
JSON/OpenAPI bindings to `.radar/contracts.json` explicitly; Python model
pointers are not accepted by the existing JSON schema reader.

Supported initial patterns:

- JSON/YAML OpenAPI direct `components.schemas.*.properties` and successful
  `application/json` responses with local schema references; JSON Schema root
  `$schema`/`properties` and direct `$defs` objects.
- Direct Pydantic `BaseModel` imports and class-body annotated fields, with
  same-file models and literal one-line FastAPI response-model decorators.
- Single named TypeScript relative imports resolving to local interface/type
  declarations and explicitly typed local `fetch('/literal').json()` values.
- Tree-sitter declaration and member-access evidence identifying fields read
  from those values. Comments, strings and method-local annotations do not
  establish fields or consumers.

`check` and `merge-check` compare discovered source states. If a declared
response field disappears and a supported candidate consumer still accesses
it, Radar reports the schema/producer/consumer evidence and suggests a
coordinated migration and integration verification. The relationship remains
inferred: an identical URL does not establish deployment origin, HTTP method
or runtime serialization. Duplicate producers remain unknown. Unrelated
schema edits do not invent an affected consumer.

Aliases, re-exports, imported Python response models, inheritance, computed
fields, runtime generation, complex/multiline decorators, dynamic URLs,
variable scope shadowing and transport behavior remain unsupported. Direct
JSON Schema definitions can be found without inventing producer/consumer
links. Schema-document locations identify line 1; source references carry
actual declaration/access lines. Coverage remains incomplete, including when
no findings are found. See the [implementation notes and fixtures](../internal/discovery/README.md).
