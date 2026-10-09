# Static contract candidates

`radar discover --json` reads tracked and untracked non-ignored regular files. It
never modifies `.radar/contracts.json`. JSON output includes evidence locations,
ambiguities, schemas, candidates and an inspectable `proposed_manifest`. Copy
reviewed JSON/OpenAPI bindings into an explicit manifest yourself. Python model
pointers cannot be accepted by the existing JSON schema reader, so they are
reported as candidates without invalid proposed manifest entries.

Supported patterns:

- JSON/YAML OpenAPI `components.schemas.*.properties`, successful 2xx response
  `application/json` schemas with a local `#/components/schemas/...` reference.
- JSON Schema root `properties` with `$schema`, and `$defs.*.properties`.
- Python `from pydantic import BaseModel`, direct `class Name(BaseModel):`
  annotated fields and same-file, one-line FastAPI-style literal URL decorators
  with `response_model=Name`.
- TypeScript/TSX single named relative imports resolving directly to a local
  interface/type declaration, and explicitly annotated local variables:
  `const user: User = await (await fetch('/users')).json();`.
- Tree-sitter declaration validation and member-access nodes identify
  `user.email` field use; comments and string contents are not field accesses.

Producer-consumer candidates require the literal endpoint and resolvable imported
local type. A type name matching a Python class name alone never binds a consumer.
Duplicate producer URLs remain unknown. All links remain proposed or inferred:
URL origin, HTTP method, variable scope shadowing, runtime payloads, casts and
middleware are unresolved. Imports/re-exports/aliases, imported response models,
Pydantic inheritance/serialization aliases and multiline decorators are outside
this initial subset. Schema locations currently point to line 1; source
relationships and field accesses carry actual lines.

Discovery and comparison remain `incomplete` with no findings, never comprehensive
success. Comparing a baseline with an integrated/working tree reports warnings
when a previously declared response field disappears while a supported consumer
still accesses it. These are actionable static candidates requiring review, not
proof of runtime breakage. Explicit accepted manifests and authorized integration
tests retain their existing stronger compatibility/observed evidence semantics.

The fixture in `testdata/fastapi` is intentionally small and requires no Python or
TypeScript runtime for analysis. Tests also cover OpenAPI, unrelated schemas,
missing imports, ambiguity and field removal without any accepted manifest.
