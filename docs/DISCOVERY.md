# Inspectable contract discovery

`radar discover --json` observes bounded source without importing applications,
starting FastAPI, running package managers, or writing approved declarations.
Results remain `incomplete`; an empty finding list does not establish runtime
compatibility. Committed refs carry exact repository revisions; WORKTREE is an
informational observation. Every source relationship carries file, line, method
and evidence category. JSON/YAML schema document locations identify line 1.

## Demonstrated discovery slice

| Pattern | Supported static subset | Trust level |
|---|---|---|
| Python model | Pydantic `BaseModel`, named aliases, local/relative imports, one unambiguous base, annotated fields and resolvable nested model fields | Observed syntax and bounded symbol resolution |
| Python endpoint | Imported/aliased FastAPI and APIRouter constructors, common HTTP decorators, literal paths and `response_model`, nested literal `include_router` prefixes | Registered source relationship; runtime registration effects unverified |
| TS type | Named/type imports and aliases, interfaces/type aliases, local type re-exports and barrel exports, bounded cycle/ambiguity handling | Canonical local declaration; no TypeScript compiler semantics |
| TS response | Explicitly typed awaited literal fetch JSON, `axios.get<Type>()`, imported Axios and simple `axios.create()` instance calls | Proposed response/type relationship |
| TS consumption | Member access and nested paths on the associated variable with lexical scope handling | Syntactic use; runtime behavior unverified |
| Schema documents | Literal JSON/YAML OpenAPI responses with local schema refs and JSON Schema properties | Document facts; accepted only after review |

Candidates match literal endpoint path and known HTTP method, with imported
canonical type provenance. A matching model/type name alone creates no link.
Duplicate producer ownership remains unknown. Deployment origin, interceptors,
casts, validators, serialization aliases and middleware are not established.
Dynamic URLs/registration, computed response schemas, complex inheritance and
ambiguous exports remain unresolved or diagnostic. See the package tests for
precise demonstrated cases; this is not comprehensive framework support.

## Evidence and acceptance

Observed syntax and resolved import chains use `verified_static` provenance;
producer-consumer transport candidates remain `inferred`, with ambiguities.
Producer-only suggestions are `proposed`; ambiguous ownership is `unknown`.
Discovery neither approves a plan nor silently creates a contract obligation.
A removed consumed source field produces an inferred warning, and does not make
`no_breaking_contracts` fail by itself.

Review `proposed_manifest` for JSON/OpenAPI bindings and explicitly save approved
ones into `.radar/contracts.json`. Python `/models/...` pointers are not JSON
Schema pointers, so they are deliberately excluded from that manifest. For
Python projects, maintain a reviewed checked-in JSON/OpenAPI response snapshot,
then declare producer, consumer, pointer and consumed fields against it. Radar
does not execute application imports to generate that snapshot. Commit reviewed
declarations and schema changes for authoritative checkpoint comparison. Baseline
obligations persist when a head removes or malforms its declarations.

## Reproducible quality fixture

[FastAPI/TypeScript example](../examples/fastapi-typescript/README.md) contains
separate models, nested routers, aliased imports, fetch and Axios consumers,
barrel types, and dynamic/unrelated negatives. Its test reports found ground
truth, false candidates, unresolved diagnostics and elapsed discovery time:

```sh
go test ./internal/discovery -run 'TestRepresentativeDiscoveryQuality|TestReviewedExampleBindingAndRecommendations' -v -count=1
```

On the local Linux host: 2/2 ground-truth consumer relationships found, zero false
consumer candidates, one dynamic-URL unresolved diagnostic, approximately 21 ms
for discovery (single illustrative run, not a performance guarantee). Regression
fixtures additionally cover ambiguity, cycles, comments/strings, method-local
fields, constructor shadowing and scoped response-variable shadowing. Reviewed
bindings detect a committed removed nested field authoritatively, and Python/Node
snapshot tests are recommended. An additional opt-in full candidate gate ran a real FastAPI TestClient/Pydantic
GET response with pinned versions plus Python/Node snapshot tests. No production
deployment or live TypeScript/Axios transport compatibility is claimed.
