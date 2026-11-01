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
| C / C++ | declarations, `#include` | in order: the including file's directory (quoted includes); `-I`, `-iquote` and `-isystem` of a committed `compile_commands.json`; ancestors up to the nearest build file (quoted); `include/`, and for quoted includes `src/`, of each enclosing CMake, Meson or Make project |

Parsing is Tree-sitter, built into the binary. Static analysis runs no compiler;
opt-in [type checking](#type-checking) is separate.
Supported literal symbol relationships are inferred; opt-in file observation is
separate from static analysis. Import edges (`DEPENDS_ON`) are labeled
`inferred`: they follow each language's path conventions without a compiler.

Literal workspace/project references for supported manifests are resolved with
`workspace_member` provenance. Not handled: package-based tsconfig `extends`,
`node_modules`, arbitrary PYTHONPATH, Go vendoring and dynamic imports. Two repository packages with the same name are left unresolved.
Imports from outside the repository stay as unresolved `module` nodes.
TypeScript variance annotations (`interface X<in T>`) are not parsed by the
bundled grammar; those declarations are skipped with a warning.

Java types used from the same package need no import, so they get no edge;
test selection relates same-package tests instead. A type declared by two files
is left unresolved. Fully qualified names used without an import, reflection,
generated sources (annotation processors, Lombok) and Kotlin are not handled.

C and C++: `.c` is C, `.cc`, `.cpp`, `.cxx`, `.hpp`, `.hh` and similar are
C++, and a `.h` header is C++ only when the C++ grammar parses it cleanly and
the C grammar cannot or the text uses C++-only syntax. A header is linked to
the one implementation file with the same name beside it or in the mirrored
`src/` of an `include/` layout (`header_source_pairing`, inferred), so a change
to `foo.cpp` reaches the tests that include `foo.h`. Macros and `#if` branches
are not evaluated: every branch is indexed, and a computed `#include` stays
unresolved. CMake, Meson and Make scripts are not evaluated, so
`target_include_directories` is unknown unless `compile_commands.json` is
committed, and its flags apply only to the files it lists; headers it does not
list resolve by the conventions above. A header found in both `include/` and
`src/` is left unresolved. System headers are not indexed.

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
Bounded literal Protobuf and GraphQL schemas are supported; unsupported grammar
remains unknown. Manifest-declared `generate` argv can produce specs only after
explicit execution opt-in. Network-call tracing is not supported. See
[the supported subsets](USAGE.md#contracts-and-generated-outputs).

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

## Migration order

When the branches change a migration history, `gate` reads the migration files
at the base, each branch merged alone onto the base (`git merge-tree`, Git
2.38 or later) and the combined tree, and requires `migration_order`. Nothing
is run.

| Tool | Location by convention | Failure |
| --- | --- | --- |
| Alembic | `*/versions/*.py` next to `env.py` | more than one head on one Alembic branch, or a reused branch label (`revision`, `down_revision`, `branch_labels`) |
| Django | `*/migrations/*.py` with a `Migration` class | more than one leaf per app (`dependencies`) |
| Flyway | `V<version>__*.sql` in one directory under `db/migration/` | one version used twice |
| Versioned SQL (golang-migrate) | `<version>_*.up.sql` / `.down.sql` in one directory | one version used twice |

SQLx also recognizes `migrations/<version>_*.sql` by convention. Explicit
`goose` locations support numbered SQL files; Go registration remains unknown.
Alembic literal `depends_on` references to revisions or unique branch labels
are validated for missing dependencies and cycles, separately from branch heads.
Declared TypeORM locations compare directly exported migration classes. Private
helpers are excluded; aliases, re-exports and computed exports remain unknown.

A problem the base already has is a warning, including when a branch only
renames or removes part of it. Any other problem fails; the report says
whether one branch merged alone already causes it or whether it appears only
when the branches are combined. A new Flyway or SQL version below
the newest base version is a warning: databases already past it skip or reject
it. A revision with `branch_labels` starts a named branch, so it is not a second
head. A `migrations/` directory without a Django `Migration` class is not
judged.

Values must be literals assigned once. A computed or reassigned `revision`,
`down_revision`, `branch_labels` or `dependencies`, a parent outside the
history, a nested Alembic version directory, a squashed Django migration whose
replaced migrations still exist, an ambiguous Django app label, or a file over
1 MiB makes the check `unknown` when it is in a history the branches changed.
A Flyway version that a changed directory shares with another directory under
the same `db/migration/` is `unknown`, because configured locations decide
whether both load; directories the base already uses in parallel, such as one
per database vendor, are compared separately. Literal `alembic.ini` script and
version locations are read even when only revision files change.
`MIGRATION_MODULES` and Flyway configuration are not read. Dependencies outside the recognized Alembic history,
Django `run_before` across apps, Java and other non-SQL migrations, and
conflicting schema operations in separate migrations are not compared.
`radar check` compares one head and does not run this check.
[`examples/migrations/demo.sh`](../examples/migrations/demo.sh) runs the
two-agent case end to end.

## Tests

Recognized result formats: `go test -json`, `python -m unittest`, `pytest`,
Jest, Vitest, `node --test`, `cargo test`, Maven Surefire totals, CTest and
GoogleTest summaries, the JUnit reports Maven, Gradle and CTest write for
selected commands, and any JUnit XML report
declared with `junit`: one file, or a directory of `TEST-*.xml` files.
Inventory, selection and result interpretation are described in
[verification](VERIFICATION.md#test-selection).

## Type checking

`radar gate --run --typecheck` type-checks the combined tree before tests run.
It is part of the execution opt-in and is never available from MCP.

| Language | Command | Target |
| --- | --- | --- |
| Go | `go vet -buildtag ./...` | each `go.mod` module that owns a changed `.go`, `go.mod` or `go.sum` file, C or assembly input, or file beside Go source; modules that use a changed or deleted module through `go.work` or a relative `replace` |
| Python | `python3 -m mypy …`; `ruff check --select F821,F811,F822,F823 … FILES` | the nearest directory above a changed `.py` or `.pyi` file whose `mypy.ini`, `.mypy.ini`, `setup.cfg` or `pyproject.toml` configures mypy, or whose `ruff.toml`, `.ruff.toml` or `pyproject.toml` configures ruff |

The command runs in its own private copy of the candidate, so it cannot change
what tests observe. A command that changes files there is discarded. Module
downloads are disabled, so a missing third-party module, C header or C compiler
is an environment problem. A missing package of a repository module is a source
failure, and so are import cycles and two package names in one directory.
`-buildtag` also reports malformed build constraints.

Python tools are the ones on PATH: mypy runs as a module of `python3`, the
interpreter tests use, so activate the project's environment first. As for
tests, an import path that reaches outside the private copy (for example an
editable install of your checkout) stops the run. mypy checks what its
configuration names (`files`, `packages`, `modules`); otherwise it checks the
whole project (`.`), so unchanged modules that import changed or deleted code
are checked too. mypy reports which
files it checks; changed files it leaves out (`exclude`, `ignore_errors` or a
module-wide `# type: ignore`) are listed as not type-checked. mypy's own settings
decide how deeply it checks, for example
whether it checks the bodies of unannotated functions. ruff is given the
changed files and only looks for undefined and redefined names in them. A
third-party module or stub package mypy cannot find is an environment problem;
a missing module of the project itself is a source failure. Neither tool writes
a cache. Changed Python files without mypy or ruff configuration are listed in
the report and are not type-checked.

A failure is compared with the same command on the base. Errors are matched by
file and message, not line, and an error in a file a branch only moved still
matches its base error. On the base, ruff is given the changed files that
exist there, plus the files a branch deleted or moved. `go vet` stops at the first error of a package and skips packages
that import it, and mypy stops at a syntax error, so when such a base run
already fails, a new-looking error may have been hidden there: the result is
not verified instead of failed. If the base cannot be type-checked, or the project
does not exist there, the errors are kept and the gate fails, with a note that
some may already exist on the base.

After each run, `go list` in the same environment reports which files the
toolchain compiled. Changed Go files it did not compile are listed as not
type-checked and keep the check from passing: files excluded by their name,
build constraints, release or custom tags, cgo files without a C compiler, and
directories `./...` skips. Files under `vendor` or outside every module are not
type-checked either. Unchanged files the toolchain does not compile are not
checked, so an error that appears only for another GOOS, GOARCH or tag is not
found. When no changed file belongs to a supported
project, no type-check requirement is added and the report says so. A pass is a
type checker's result, not observed behavior.

## Parallel work and landing

`land` reuses supported static checks for pair conflicts and ancestry-aware
ordering. Its rebase count is an estimate, not observed workflow performance.
Pair bounds and unsupported relationships remain unknown. Conflict briefs expose
bounded committed hunks, inferred symbol/test links and reproduction arguments;
they do not establish coverage. Explicit CLI opt-in can run an installed
`mergiraf`/`weave` in private state or publish `refs/radar/integration/*` after
complete-candidate static verification. User branch movement remains explicit.

`watch --include-dirty`, `gate --overlap` and Stop hooks inspect saved peer
worktree drift. These are static repair leads, not a lock or runtime guarantee.
The hook only blocks new drift caused by the current worktree's saved changes;
pre-existing textual conflicts/drift are informational. Doctor's bounded local
resource advisories compare literals privately, keep distinct values in separate
worktree groups, and leave expressions unknown.

Passing gate trailers refer to immutable local historical reports; `show` does
not revalidate current commits or authenticate a signer. Actions check runs and
report rendering are described in the [integration](../integrations/github-actions/README.md)
and [report package](../integrations/report/README.md) guides.

Explicit `index --scip ... --scip-binding ...` adds producer-asserted occurrences
after checking revision, index digest and every indexed source digest. It does
not run a compiler, authenticate the producer or replace candidate analysis.
A bounded content-based syntax cache reuses unchanged parsing; import resolution,
manifest reads and diagnostics are recomputed. This is not incremental updating
of the entire dependency graph. See [Usage](USAGE.md#external-scip-input).

## Plans

Plans hold requirements, decisions, constraints, contract and graph deltas,
tasks with dependencies, acceptance criteria and assumptions. Verification
rules can check file existence, JSON/YAML properties, indexed entities, exact
schema objects and `test_run` evidence. A property-presence rule proves the
property is there and nothing more.

## Not supported

- type checkers other than `go vet`, mypy and ruff (TypeScript, Rust, Java and C++ type checking)
- incremental updating of the entire dependency graph
- published Windows binaries (native qualification jobs are configured, not demonstrated by local Linux checks)
- release signing or notarization
- an OS sandbox for executed tests
- automatic dependency installation or `git fetch`

## Future work

Cross-repository runtime scenarios, TypeScript project references and package-based
`extends`, symbol-level Python imports, Java endpoint discovery,
configuration-aware C/C++ includes and per-test CTest selection remain future
work. Task-footprint Split prediction is conditional on interviews or observed
drift showing a useful prediction opportunity; no new predictive command is
implemented. A hosted GitHub App is conditional on self-host adoption and is not
implemented. Agent orchestration, dependency installation and automatic remote
fetching remain out of scope.


Configuration examples are in [Usage](USAGE.md#advanced-configuration).

### Proposed release sequence

This table describes follow-up work and acceptance targets, not shipped commands
or a release schedule. Phase 0 stabilizes existing behavior for 0.3.0; publication
still requires the native CI, benchmark and installation evidence in the release
guide. Retracted 0.4.0/0.4.1 versions must not be reused.

| Phase / target | Dependency and proposed work | Completion evidence |
| --- | --- | --- |
| 1 — Land / 0.4.2 | Phase 0 landing scope and restack classification; chain restack guidance and hunk-related tests. | Deterministic independent, pair-conflict, migration-chain and base-conflict fixtures; installed mergiraf/weave qualification. Rebase remains advice. |
| 2 — Work / 0.5.0 | Hook stage measurements; incrementally update syntax, affected nodes/edges and imports; lightweight post-tool textual checks. | Saved changes reach watch drift within 5 seconds; a cold 1600-file, 12-worktree hook responds within its 20-second budget on a stated machine. |
| 3 — Prove / 0.6.0 | Stable candidate evidence; real GitHub checks, required-check configuration and FE/BE static contract pairs. | Check attached to exact SHA; stale, excluded-input and other-candidate evidence rejected; blocked required checks prevent merge. |
| 4 — Queue / 0.7.0 | Prove check publication; trusted `merge_group: checks_requested` helper and separate execution argv/policy requirements. | Publish to the group SHA; after removing a failing PR rerun the entire remaining combination before showing subset PASS. |
| 5 — Split / conditional | Interviews and observed drift establish a useful prediction opportunity; task/AGENTS footprints and imports suggest parallel work. | Reproducible useful predictions with counterexamples; inferred suggestions never become compatibility evidence. |
| 6 — ongoing foundation | Detector contribution rules, TS monorepo fixtures, opt-in SCIP, large-repository limits and native CI. | Every detector has positive and benign fixtures; Windows distribution follows native release qualification. |

Queue must define deduplication of repeated attempts and avoid assigning a combined
failure to every individual PR before implementing its attempt budget. A policy
file never grants execution permission. GitHub's event contract is documented in
[the official merge-group reference](https://docs.github.com/en/actions/reference/workflows-and-actions/events-that-trigger-workflows#merge_group).


Configured Python projects that import changed projects are selected transitively.
The mypy probe reports the actual repository search bases; arbitrary path suffixes
are not treated as local modules. Typed installed copies or stubs that can supply
deleted project modules stop verification as an environment provenance problem.
When mypy suppresses a deleted local import, Radar reports a syntax-based inferred
finding and incomplete checking. Comments and string literals are not imports.

Additional type-check adapters support local TypeScript compilers, offline Cargo
workspaces, literal Maven/Gradle JVM layouts, and committed gcc/clang compilation
databases. Unsupported layouts remain explicit gaps. TypeScript project references
and composite builds are unsupported; C++ response files and unrelocatable build
paths are unsupported. Actual compiler input inventory is required for a passing
check. Changed Cargo manifests/lockfiles, Maven and Gradle build configuration,
and compilation databases select their build units; unsupported or deleted
configuration leaves explicit gaps. Listing-only declared Ruff commands cannot
establish type-check coverage. A changed header selects the translation units
whose committed literal
`#include` chains can reach a file of that name. A computed include or a quoted
include of an uncommitted file selects every unit; an angle-bracket include of a
generated header is not followed. A missing C/C++ header is an environment
problem unless it resolves, where the compiler searches, to a repository file
the combined change deleted. Under
`--locked`, a repository `Cargo.lock` that no longer matches the manifests is a
source failure. Cargo/JVM native toolchain qualification and user macOS
measurements must be reported separately from parser and synthetic fixture tests.
