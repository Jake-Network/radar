# Test selection and execution

Radar picks tests from static evidence and runs them only when you ask
(`gate --run`, or `merge-check --verify --allow-execution`). A selected test
is related to a change. That does not mean it covers the change's behavior.

## Inventory

Radar recognizes conventional test files and configuration for Go, Python
unittest and pytest, `node --test`, Jest, Vitest, Cargo, Maven and Gradle. It
reads files as data: no Python imports, no package scripts, no config or build
script evaluation. Inventory
reads stop at 10,000 files, 1 MiB per file and 32 MiB in total, and anything
skipped is listed in the report.

Unknown JavaScript frameworks, and TypeScript tests for `node --test` without a
recognized loader, are inventoried but not run. They are reported as
`unsupported_configuration` and block a required run.

### Java

A Java test is a file under `src/test/java/` or named like Surefire's defaults
(`Test*`, `*Test`, `*Tests`, `*TestCase`, `*IT`) that has a test annotation or
extends `TestCase` outside comments. The nearest `pom.xml` or Gradle build file
decides the tool; both in one directory, or neither, is unsupported. Only tests
in the module's `src/test/java` run, because that is the only test source set
the default build compiles; others are inventoried as unsupported.

- Maven: a module listed literally in an ancestor's `<modules>` (not in a
  profile) runs from that reactor root as
  `mvn -o -B -pl MODULE -am -Dtest=CLASS,... test`; otherwise from the module.
- Gradle: the project comes from literal `include` entries in the nearest
  `settings.gradle(.kts)` and runs as
  `gradle --offline --no-daemon --console=plain :PROJECT:test --tests CLASS`.
  Settings that compute or relocate projects (`projectDir`, `includeFlat`,
  non-literal `include`) make its tests unsupported.
- A committed `mvnw` or `gradlew` in that root is used instead of the tool on
  `PATH`. A wrapper downloads its pinned distribution before offline mode
  applies, so Radar runs it only when that distribution is already in
  `GRADLE_USER_HOME` or `MAVEN_USER_HOME` (`~/.gradle`, `~/.m2`); otherwise
  the command is `environment_unavailable`. A JVM must be on `PATH` either way.
  Availability requires the pinned URL's cache entry and launcher, rather
  than an archive-name directory: Gradle also needs its completion marker;
  Apache Maven's JAR wrapper needs a committed wrapper JAR and its retained
  archive. Maven `distributionType=only-script` uses its distinct URL hash
  and direct Maven home layout; an absent type is treated as the JAR variant.
  Only default cache bases/paths and plain ASCII HTTP(S) ZIP URLs are
  supported. Custom cache configuration, forced downloads/unpacking, URL
  escaping for Maven's shell wrapper and unknown variants remain unavailable.
- Java commands run in separate private snapshots of the same candidate commit
  and tree, so upstream modules, separate tiers, and command chunks cannot
  reuse an earlier command's reports.
- Classes of one module merge into one command. Results come from the module's
  `target/surefire-reports` or `build/test-results/test` (default locations
  only); with no report, the run is not counted as passed.
- A Java test that declares the same package as a changed source in its module
  is selected as `java_package_companion`: same-package types need no import.
- `full` mode runs each module's test phase or task; Surefire's include
  patterns then decide which classes run.

## Modes

| Mode | Selects |
| --- | --- |
| `targeted` | plan `test_run` commands; changed tests; tests that reach a changed file through imports or a declared contract; tests that name a changed data or schema file; Go and Java package companions; integration-named suites when a change spans several package roots |
| `balanced` (default, alias `recommended`) | `targeted` plus a package-root fallback for changed files with no direct relationship |
| `full` | plan commands plus one whole-suite command per framework and package root (`go test ./...`, `python3 -m pytest`, `cargo test`, ...) |

Preview without running anything: `radar check --base main --suite targeted`.

Each command has a tier. Commands from a direct relationship are `required`.
Package-root fallbacks are `optional`. Documentation-only changes (`.md`,
`.rst`, images, LICENSE) select nothing.

Declared contracts extend selection: a change to a producer model can reach the
contract's consumer and that consumer's tests (reason `declared_contract_impact`).
Only bindings in `.radar/contracts.json` count. Discovered candidates do not.

## Grouping

Per-file commands that share a framework, working directory, tier and runner
are merged into one invocation, up to 200 paths: `python3 -m pytest a.py b.py`,
`go test -json ./x ./y`, Jest `--runTestsByPath`, `vitest run`, `node --test`,
Maven `-Dtest=A,B` and Gradle `--tests A --tests B` for one module.
Plan commands, unittest discovery and Cargo commands are never merged.
`grouped_from` lists what was merged.

Grouped tests share a process. That is how most CI runs them, but a suite that
depends on per-file process state may behave differently.

## Budgets

- `--max-commands N` (default 16) counts commands after grouping.
  Required commands go first. The rest are listed in `selection.omitted` as
  `budget_exceeded`, and a skipped required command also appears in
  `selection.blocking`.
- `--timeout` is one budget for the whole run: 10 minutes for `gate --run`, 2
  minutes for `merge-check`, 30 minutes at most. Commands that never start
  are recorded as `time_budget_exhausted`.
- Commands run one after another in the same private candidate.

If any required command was skipped or could not start, `integration_execution`
is `incomplete` and the gate is blocked, even when every command that ran
passed. A real failure takes precedence over a skip.

## Environment

Before running, Radar checks that the runner can start: a relative runner such
as `./node_modules/.bin/jest` must exist in the candidate, and a bare command
must be on `PATH`. Untracked directories (`node_modules`, `.venv`) are not
copied into the candidate. Commands that need them are reported as
`environment_unavailable` and block the gate. Prepare the dependencies, or
declare a reviewed plan `test_run` rule with `link`.

Radar never installs anything. Runs set `GOPROXY=off`, `PIP_NO_INDEX=1`,
`CARGO_NET_OFFLINE=true` and `npm_config_offline=true`, Maven and Gradle
commands run offline (`-o`, `--offline`), and runs reuse existing local caches
(Go module and build caches, Cargo and rustup homes, the npm cache, the Python
user base, an active virtualenv, `GRADLE_USER_HOME`, the Maven wrapper's
`MAVEN_USER_HOME`, `JAVA_HOME`). Maven itself reads `~/.m2` of the account
running Radar. Gradle reads `gradle.properties` from its user home, so values
kept there are visible to the build.

## Reading results

A command counts as `passed` only when Radar recognized executed tests in its
output and none failed. The rest are classified like this:

| Output | Result |
| --- | --- |
| recognized failing tests | `failed` |
| compile error in a repository source file (`go test -json` build output, `cargo test` `error[E…]` with a repository path, javac errors from Maven or Gradle) | `failed`, with location and undefined name |
| `ImportError` for a name the combined source no longer defines, under pytest or unittest | `failed` |
| `ModuleNotFoundError`, missing toolchain, checksum or registry errors, unresolved Maven/Gradle dependencies, javac `package … does not exist` | `error` (environment) |
| compile error whose path is absolute or outside the repository | `error` (environment) |
| runner or command not found, timeout, output limit | `error` |
| exit 0 with no recognized tests | `unknown` |

When a test fails, the report names the branches whose changed files the test
imports. That is a place to start looking, not proof of which branch caused it.

## Native selectors

| Engine | Status |
| --- | --- |
| Go packages | used; selected packages are grouped into one `go test` |
| Cargo | used per crate (`cargo test --manifest-path`), no per-test filtering |
| Maven Surefire `-Dtest`, Gradle `--tests` | used per test class within one module |
| Jest `--findRelatedTests`, Vitest `related` | not used; they run repository config and need `node_modules` |
| Nx affected | not used; needs the Nx daemon and plugins |
| pytest-testmon | not used; needs a recorded coverage database |

Read-only commands, including all MCP tools, never run a selector, collection
hook or package script.

## Agent summaries

MCP `radar_check` accepts `suite` and `max_commands`. Its summary includes an
`agent_brief`: changed files, contract impact, required and optional commands
with reasons, what ran and failed, what was skipped and why, uncovered changes,
and the checks still unsatisfied. Radar does not retry or repair. The agent
fixes the code and reruns the same command.
