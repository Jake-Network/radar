# Roadmap

Nothing here is scheduled. Items move into a release when there is a fixture
that proves them and a failure case that shows the limit.

## Next

- **Watch mode.** Rerun the static gate while agents work, so conflicts show up
  before the branches are finished.
- **Observed attribution.** Rerun a failing test on subsets of the branches to
  find which combination breaks it, instead of inferring it from imports.
- **Windows binaries.** Handle `core.autocrlf` and long paths, then add Windows
  to the release matrix.
- **Workspace scenarios.** Run a declared test against every repository's
  candidate at once, and a `--workspace FILE` mode for CI.
- **Better TypeScript resolution.** Project references and package-based
  `extends`.

## Later

- Symbol-level Python import analysis, so a change behind a package
  `__init__` does not select every test.
- Native selectors (Jest `--findRelatedTests`, Vitest `related`, Nx affected,
  pytest-testmon), run under the same consent and environment rules as tests.
- Java contract discovery: literal Spring MVC and JAX-RS endpoints with their
  response types as proposed bindings.
- C/C++: include edges per compilation configuration, `compile_commands.json`
  as data, CTest and GoogleTest results.
- Compiler-backed semantics (SCIP, go/types, rust-analyzer, tsc) and
  incremental indexing.
- Signed and notarized releases.
- OS-level isolation for executed tests. Until then, use a disposable runner.

## Out of scope

- Installing dependencies or fetching from remotes.
- Launching or orchestrating agents.
