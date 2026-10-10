# Changelog

## 0.1.1 — first published release

Same behavior as 0.1.0. The `v0.1.0` tag failed macOS release qualification
because two test fixtures assumed temporary files live under `/tmp`, so no
0.1.0 binaries were published. 0.1.1 fixes those tests.

## 0.1.0

Radar checks what parallel coding-agent branches do **together** before they
merge: it combines committed branches in private Git state and reports
conflicts, declared contract changes, impact and the tests to run, and with
`--run` executes those tests on the combined tree.

### Included

* `radar gate` (single repository) and `radar workspace` gates (several local
  repositories with declared cross-repository links). Exit codes: pass 0,
  fail/blocked 1, error 2.
* Structural indexing and import dependencies for TypeScript/JavaScript,
  Python, Go, Rust, Java and C/C++ (`#include`, with a committed
  `compile_commands.json` when present). Tree-sitter; no compiler semantics.
* Explicit OpenAPI/JSON Schema contract bindings with directional
  compatibility, and static contract candidates for literal FastAPI/Pydantic
  producers and typed TypeScript fetch/Axios consumers.
* Test inventory and selection for `go test -json`, unittest, pytest, Jest,
  Vitest, `node --test`, `cargo test`, Maven, Gradle and CTest (with
  GoogleTest), and declared JUnit reports.
* Plans, preflight, task DAGs, digest-bound review declarations and
  commit-bound verification evidence.
* `radar setup` for Codex and Claude Code, and an MCP server without test
  execution or review tools.
* Binaries for Linux and macOS (amd64, arm64). Windows binaries are not
  published yet.

### Fixed since the pre-release `v0.4.0` tag

* Shallow clones: the private candidate now shares the clone's history
  boundary, so gates work when the needed history is present; when it is not,
  the error says to fetch full history (`git fetch --unshallow`, or
  `fetch-depth: 0`) instead of `git merge: exit status 128`.
* Branches that do not compile together are a `FAIL` with the location and
  the undefined name (Go `go test -json`, Rust `cargo test`, javac through
  Maven or Gradle, GCC/Clang and GNU ld), not an environment error. Missing
  modules, dependencies, toolchains and registry crates stay environment
  errors. Under unittest, an `ImportError` for a name the combined
  source no longer defines is a failure, as it already was under pytest.
* With no declared contracts, the terminal shows `· Contracts none declared`
  instead of a green check; JSON output is unchanged.

### Known limits

* Syntax-level analysis: no type checking, call resolution or runtime tracing.
  Tree-sitter TypeScript 0.23 does not parse variance annotations
  (`interface X<in T>`); those declarations are skipped with a warning.
* Java: same-package types get no import edge (tests in the same package are
  selected instead); reflection, generated sources and Kotlin are not handled.
* C/C++: macros and `#if` branches are not evaluated, and CMake, Meson and
  Make scripts are not run, so include directories are known only from a
  committed `compile_commands.json` or conventional layouts.
* Contract checks need declared bindings in `.radar/contracts.json`;
  `radar discover` only proposes candidates.
* The candidate directory is not an OS sandbox, and Radar never installs
  dependencies.

### Versioning

Pre-release development used milestone numbers 0.1–0.4. A `v0.4.0` tag
was pushed but never published, and Go's module proxy cached it, so `go.mod`
retracts v0.4.0 and v0.4.1. Use v0.1.1 or later. See
[RELEASING](docs/RELEASING.md).
