# Changelog

## Unreleased

* Continuous single-repository verification with `radar watch`, including
  opt-in private snapshots of saved changes with `--include-dirty`. JSONL
  events invalidate stale results; unsaved editor buffers and ignored
  dependencies remain outside the preview.
* Additional attribution reruns are off by default for both `radar gate --run`
  and `radar watch --run`. Use `--attribution-runs N` to opt into bounded
  diagnostic observations. Inferred repair leads remain available by default.
* Dirty watch branch reports include staged, unstaged and untracked paths,
  including renames and deletions, alongside committed changes. Included-commit
  metadata is accurate, and incomplete file inspection is reported explicitly.
* Doctor reports inaccessible worktrees as incomplete instead of claiming
  there are no uncommitted changes. Git test fixtures no longer depend on
  a developer's global author identity, fixing the main CI failure.
* Agent guidance distinguishes committed Gate inputs from dirty Watch
  snapshots and preserves explicit authorization for repository code execution.

* Simpler first run. Default help shows six commands. `radar doctor` reports
  the base, the worktree branches `radar gate` would combine, uncommitted
  worktrees, detected languages and test frameworks with whether each runner
  program is on PATH or missing from the private copy, declared contracts,
  installed agent skills
  and the next command (`project` in `--json`; existing fields unchanged).
  `radar setup claude|codex|both` is the same as `--agent`. `radar help
  agents` prints the agent contract; see `docs/AGENT_GUIDE.md`. The
  interactive menu lists worktrees with uncommitted changes.
* Clearer gate reports. Each blocked requirement is grouped by cause (runner
  unavailable, command budget, time budget, incomplete inventory, unsupported
  tests, no related test) with one `fix:` line, and `Next:` names the specific
  remedy when one cause explains the block (`--max-commands N`, install a
  runner, `--suite full`). Duplicate "not run" lines for one command are gone.
  A failed combined test gets a `reproduce:` command that rebuilds the same
  commits in a throwaway worktree. Suggested reruns keep `--max-commands` and
  `--timeout`.
* Errors say what to do next: no branches to combine, an unknown branch, and a
  symlink or submodule in the combined tree print a `Next:` line, and `--json`
  and MCP add `next` beside `error`. MCP compact error output now keeps
  `error` and drops the empty `agent_brief`.
  A runner missing from the private copy is reported as absent, not "outside
  the candidate".
* `radar watch` uses the gate's report and verdict words without printing the
  verdict twice, prints the idle state as text, lists only worktrees with
  uncommitted changes, and prints a one-line heartbeat. The JSONL envelope is
  unchanged.
* A documentation-only change under `radar gate --run` still has no test
  evidence and stays `NOT VERIFIED` (exit 1, JSON unchanged), but the report
  now says that only documentation changed, so no test was selected or run,
  and points at the static `radar gate` result.
* `radar gate --run --attribution-runs N` reruns a failed test command on the base, each branch
  alone and larger combinations, and names the smallest combination that fails
  the same way while every smaller one passes (`observed_attributions` in
  `--json`). A failure already on the base names no branch. `--attribution-runs
  N` bounds the reruns (default 0; for example, opt in with `8`); they share the
  `--timeout` budget and never change the gate verdict.

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
