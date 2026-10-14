# Supported static checks and declared execution

`radar gate` compares committed branches in private Git state. It does not fetch,
install packages, import repository Python, evaluate build scripts or run tests
unless execution was explicitly authorized. Unsupported input is reported as
unknown; a static PASS covers only supported required checks.

## Workspace imports and symbols

The shared worktree/commit indexer reads literal uv/Poetry, npm/pnpm/tsconfig,
Cargo, go.work, Maven and Gradle workspace declarations. Member edges use
`workspace_member` provenance and remain inferred. Literal member globs are
bounded; computed lists, escaping paths and ambiguous package owners are unknown.
uv/Cargo exclusions are honored. These edges improve affected-file and test
selection; they do not establish runtime import behavior.

`no_symbol_conflicts` compares supported declarations and explicit named imports
against the base, including deleted defining files and literal re-export chains.
Simple, explicitly bound newly added calls can reveal argument-count changes.
Duplicate definitions require a supported common namespace. Python modules are
separate namespaces; valid interface merging, rebinding, overloads and Java test
source sets are not treated as duplicate production definitions.

Unsupported receivers, dynamic exports and ambiguous resolution are retained in
an optional `symbol_resolution: unknown` check and per-file diagnostics. They
are excluded from the supported conflict scope. Missing/partial source parsing
blocks a supported check where the necessary declarations cannot be established.
C++ cross-translation-unit function ownership and complex overloads remain outside
this static scope. No relationship is compiler-resolved.

## Migration, dependency and uniqueness checks

Conventional migration locations are detected automatically. Explicit locations:

```json
{"version":1,"locations":[{"path":"db/changes","tool":"alembic"}]}
```

Save as `.radar/migrations.json`. Tools include Alembic, Django, Flyway,
golang-migrate/SQL, sqlx, diesel, Prisma and Liquibase XML. Base defects are warnings;
new problems are compared with each branch merged alone onto the current base.
Prisma chronology is a warning, not a duplicate timestamp failure. Liquibase
includes/computed identities and ambiguous cross-file identity remain unknown.
Migration contents are not executed and SQL operation compatibility is not inferred.

`lockfile_consistency` compares direct dependency names against supported uv,
npm/pnpm and Cargo locks, or exact required Go module/version sums. Workspace
members use their own importer/project declarations. Literal Cargo workspace
inheritance is supported. This is neither dependency solving nor arbitrary
version-range compatibility. Diverging literal major-version changes are warnings.

`unique_keys` checks supported literal application route scopes for FastAPI,
Express, Gin, Spring and axum. Dynamic composition remains unknown. Explicit keys:

```json
{"version":1,"rules":[{"name":"error-code","files":["src/**/*.py"],"pattern":"ERROR_CODE = \"(E[0-9]+)\""}]}
```

Save as `.radar/unique.json`. A regex's first capture group is the key; without a
capture, the whole match is the key. Alternatively `json_pointer` can select a
string, string array or object keys. Regex rules intentionally may match comments;
choose narrow globs and patterns. Built-in route extraction excludes source-like
text inside comments and string literals. Unrelated handler/config edits do not
activate existing unsupported route histories.

## Contracts and generated outputs

Declared contract schemas can be JSON/YAML, `.proto`, `.graphql`, `.gql` or
`.graphqls`. Protobuf supports bounded literal message/enum definitions, numbers,
cardinality/types and reserved names/ranges. GraphQL supports literal named types,
fields/arguments/enums and supported nullability changes. Unsupported imports,
services/options/complex Proto constructs or GraphQL interfaces/unions/directives
and recursive types remain unavailable. Invalid or unresolved types do not pass.

`.radar/generated.json` declares generation:

```json
{"version":1,"generators":[{"name":"client","sources":["api/*.json"],"outputs":["client/**/*.ts"],"command":["python3","tools/generate.py"],"cwd":"."}]}
```

Static checks warn about stale source/output relationships. With `--run`, each
command runs in a separate private candidate. Matching output bytes establish
`generated_drift`; changes outside declared outputs invalidate the observation.
A contract binding may also declare `"generate":["python3","tools/spec.py"]`.
Base and combined specs are generated separately, with checkpoint-bound
`generated_executions` metadata and digests. Neither command success nor a digest
means tests ran.

## Runner profiles and observations

`.radar/runners.json` example:

```json
{"version":1,"profiles":[{"root":".","prefix":["uv","run","--frozen"],"setup":[["uv","sync","--frozen"]],"lockfiles":["uv.lock"],"artifacts":[".venv"],"exclude":["-m","not integration"]}]}
```

Only `--run` (or the equivalent explicit merge-check opt-in) runs declared setup.
Explicit profile setup may download dependencies from registries; it keeps the
private scrubbed environment but removes package-manager offline switches.
Regular verification and legacy plan setup retain their offline policies.
Setup, generators and selected tests share the command budget; skipped required
operations block the gate. Reviewed plan setup also consumes this budget. A
source-changing setup cannot publish reusable artifacts. Setup and cache hits
never count as passing tests.

Dependency cache entries bind repository, profile, platform and lockfile bytes;
compiled `target` artifacts additionally bind the source tree. Supported artifacts
are `.venv`, `node_modules`, `target`. The default cache limit is 512 MiB, with LRU
eviction and a configurable maximum of 10 GiB. Unsafe symlinks, sensitive files
and over-budget artifacts are rejected. Virtualenv console/editable paths are
relocated to the new private copy. Real package-manager/network/platform behavior
still needs project-specific execution evidence.

`radar gate --run --observe-coverage` observes Python file reads, including data
files and child Python processes. Other frameworks declare a fresh report in the
profile, for example `"coverage":{"format":"go","report":"coverage.out"}`
with an explicitly declared argv argument such as `-coverprofile=coverage.out`.
Formats are coverage.py JSON (`python`), Go coverprofile (`go`), JaCoCo XML
(`jacoco`), llvm-cov export JSON (`llvm`), and Istanbul/c8 JSON (`c8`). Report paths
are relative to the selected command's CWD. A report already present before the
command cannot supply fresh evidence. Reports are bounded and raw contents are
not persisted.

SQLite records associate an observed command suite with its selected test files,
not individual cases. Selection uses only relationships whose test/file bytes
match the baseline and whose recorded baseline is an ancestor of the current
base. Stale and unrelated records remain excluded. Coverage is saved only after
all executions establish unchanged candidate source. A fresh relationship augments
static impact; it never proves complete behavioral coverage.
