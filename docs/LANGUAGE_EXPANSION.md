# Next language slices (proposal, not implemented)

Recommend **Java first**. This is an engineering judgment: Java backend projects
can reuse Radar's producer/consumer contract model and existing JUnit evidence
reader. Module/package conventions offer a narrower first slice than C/C++
preprocessing and build-configuration-dependent header resolution. Neither
recommendation is measured market demand; validate it with users and pinned
multi-module projects before committing a release scope.

| Choice | Expected user value | Main risk | Verification fit |
| --- | --- | --- | --- |
| Java | Parallel backend/service changes and shared DTO/module boundaries | Gradle executable configuration, generated code, ambiguous packages | JUnit observations and module suites fit existing candidate evidence |
| C/C++ | Shared headers and native library changes, including Dense projects | Macros, include search order, multiple compile configurations | Needs configuration-specific dependencies and explicit build/test phases |

## Java vertical slice

1. Pin and license-review [Tree-sitter Java](https://github.com/tree-sitter/tree-sitter-java).
   Extend `internal/languages/adapter.go` for `.java`, with fixture tests for
   classes, interfaces, records, enums, nested types, annotations, imports and
   syntax errors. Maintain bounded parser reads and `semantic: false`.
2. Retain existing location-based IDs (`type:path#package.Outer.Inner`). Add
   package/module attributes rather than replacing existing IDs. Method IDs
   need a syntactic parameter signature to distinguish overloads; this does
   not establish erased/generic/compiler identity. Preserve declaration
   provenance and distinguish explicit from wildcard/static imports.
3. Add a source-only module-root descriptor used by both indexer and selection.
   Read Maven `pom.xml` module lists and ordinary source/test roots with bounded
   XML parsing and external entities disabled. Read only supported literal
   Gradle settings/module declarations; never evaluate Groovy/Kotlin scripts,
   wrappers or plugins in static mode. Custom source sets and computed modules
   are explicit unknowns, not guesses.
4. Resolve local fully qualified imports within discovered source roots and
   module identities. Same-package and wildcard candidates may create inferred
   dependencies only when unambiguous. Duplicate classes, unresolved generated
   sources, annotation processors, reflection, dependency injection and external
   JARs remain unavailable semantic/runtime relationships.
5. Inventory JUnit annotations and conventional test roots, separating source
   test discovery from executable cases. Start with full affected-module suites,
   then opt-in targeted class filters. Propose argv/CWD without invoking Maven,
   Gradle or repository wrappers; wrapper execution requires the normal explicit
   execution authorization. Dependency preparation remains operator-owned.
6. Consume fresh candidate-bound JUnit XML through `internal/evidence`.
   [Surefire](https://maven.apache.org/surefire/maven-surefire-plugin/test-mojo)
   report configuration must be captured alongside argv/module roots. Missing
   reports, zero recognized tests, stale reports and incomplete reactor execution
   cannot satisfy required evidence. Keep integration-test phases distinct from
   ordinary unit tests; `mvn test` alone does not imply all project CI passed.

Architecture additions: a shared module/source-root descriptor, Java import
resolution in `internal/indexer`, conventional Java inventory/planning in
`internal/testselection`, and exact module-command/report bindings in
`internal/evidence`. Extend capabilities and coverage schema additively.
The existing gate evaluator and committed candidate isolation remain reusable.

Acceptance fixtures: Maven parent with API/DTO/consumer modules, Gradle literal
multi-project build, nested test roots, duplicate package/type names, wildcard
imports, inherited JUnit tests, generated-source unknowns and a two-agent DTO
change where both branches pass individually but combined existing tests fail.
Compare candidate detection and false blocking with the project's full CI.

## Separate C/C++ vertical slice

1. Pin Tree-sitter C and C++ grammars independently. Index functions, types,
   namespaces, templates, declarations and literal includes. Header extension
   alone cannot identify language; use explicit project configuration or report
   ambiguity. Syntax parsing cannot establish template instantiations or ABI.
2. Record include edges with search-root and compilation-configuration identity.
   Quoted includes can resolve against local directories only when supported;
   system includes, macro-expanded includes, include-next and conditional includes
   remain bounded unknowns without compiler evidence. A header may affect many
   translation units with different definitions.
3. Read [compile_commands.json](https://github.com/llvm/llvm-project/blob/main/clang/docs/JSONCompilationDatabase.md)
   as bounded data. Bind directory, translation unit, arguments and input digest;
   prefer structured `arguments`. Do not execute its commands or perform shell
   expansion. Absolute build paths need explicit reviewed root mappings into
   the private candidate. Multiple commands for a file remain distinct variants.
4. Discover literal CMake/CTest roots without evaluating CMake. Configuration,
   generation, compilation and tests are separate explicitly authorized phases.
   Never treat a preexisting build directory as proof for changed committed
   source. Require reviewed preparation and candidate-bound build inputs.
5. Propose CTest suites and consume fresh JUnit/CTest results. GoogleTest source
   macros identify proposed tests only; listing a binary with `--gtest_list_tests`
   executes code and requires opt-in. Bind executable/build provenance and
   GoogleTest XML to the candidate and exact filter command. Zero tests and
   missing build outputs remain unknown/unavailable.

Architecture additions: configuration-keyed dependency edges, build-artifact
provenance, optional compilation-database provider and preparation evidence.
Reuse current argv execution and gate requirements; never collapse one build
variant into comprehensive coverage for every preprocessor configuration.

Acceptance fixtures: C and C++ libraries sharing headers, CMake multi-target
build, conditional compilation with two variants, generated headers, conflicting
include directories, inline/template changes, GoogleTest failures, missing
compile databases and a combined branch failure. Ship structural/header support
only with explicit limitations; compiler-backed semantics are a later slice.
