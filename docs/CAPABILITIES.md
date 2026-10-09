# Supported capabilities

| Language | Structural indexing | File dependencies | Semantic indexing |
| --- | --- | --- | --- |
| TypeScript / TSX | Tree-sitter declarations and imports | Relative specifiers (`./x`, `../x`, ESM `.js` → `.ts`, `index` files) | Unavailable |
| JavaScript / JSX | Tree-sitter declarations and imports | Relative specifiers | Unavailable |
| Python | Tree-sitter declarations and imports | Absolute modules from the importer's directory, the repository root or `src/`; relative imports; `from pkg import submodule` | Unavailable |
| Go | Tree-sitter declarations and imports | Packages inside any `go.mod` module of the repository (non-test files) | Unavailable |
| Rust | Tree-sitter declarations and imports | `crate::`, `self::`, `super::` paths and `mod name;` declarations | Unavailable |

Entities carry repository, revision, source location, method, and evidence.
Entity IDs are readable and location independent (`kind:path#Qualified.Name`).
Syntactic declarations are static evidence. `DEPENDS_ON` file edges are
`inferred`: they follow each language's path conventions without a compiler,
tsconfig `paths` aliases, PYTHONPATH, Go vendoring or Cargo workspace metadata.
Package imports from outside the repository stay unresolved `module` nodes.
Indexing does not typecheck, establish runtime calls, resolve dynamic imports,
or prove authorization behavior. Syntax errors are diagnostic evidence.
`radar doctor` reports capabilities; these grammars are compiled into the binary.
Working-tree indexing honors `.gitignore` inside a Git work tree.

Contract analysis uses JSON or YAML OpenAPI documents and JSON Schema with
explicit producer/consumer bindings in `.radar/contracts.json`. Checked:
removed properties; type changes including nullability (`nullable`, type
arrays, `anyOf`/`oneOf` with `null`); required additions/removals; enum value
additions/removals; changed validation constraints (`format`, lengths, ranges,
`additionalProperties`, ...); local `$ref`, recursive references and `allOf`.
Annotations are ignored. Several structured alternatives, `not`,
conditionals, tuple items and external references are not analyzed: unchanged
they are ignored, changed they are reported as unanalyzed and the report
status is `incomplete`. It is not a complete OpenAPI compatibility
implementation. Protobuf, runtime FastAPI schema generation and network-call tracing are unavailable. Static candidates are described in [discovery](DISCOVERY.md). Consumer field dependencies are
declarations, not verified runtime access; `radar contracts` flags declared
fields that do not appear in the consumer source (a lexical check).

Plans support requirements, decisions, constraints, contract and graph deltas,
tasks, DAG ordering, contract ownership conflicts, acceptance criteria and
assumptions with an open/accepted/resolved lifecycle. Preflight returns
`next_steps`. Without a reasoning agent, `radar plan` creates grounded context
(lexical matches plus import and explicit contract neighbours) and labels design fields
incomplete. It does not invent an architecture.

Verification rules check file existence, JSON/YAML properties, indexed graph
entities, and explicit `test_run` command evidence. Approved contract deltas can
compare an exact schema object at a declared JSON pointer. Undeclared changes to
bound schemas are reported as drift. Passing a property-presence rule proves
presence only; it does not prove tenant isolation, job durability, scalability,
or execution behavior. Working-tree analysis is informational. Digest-bound
local review declarations and commit-pinned implementation checks support
authoritative reports. Stale test evidence cannot satisfy a different plan or
implementation commit. Unsupported checks remain unknown. A successful test
command is only evidence for its declared criterion; Radar does not inspect
test quality or guarantee runtime correctness.

Recognized test results: Go `test -json`, Python `-m unittest`, `pytest`,
`jest`, `vitest`, Node `--test`, Cargo `test`, and JUnit XML reports declared
with `junit`. A successful command with zero recognized executed tests yields
unknown; a command failing before any recognized result yields error;
timeout, output-limit or incomplete execution cannot become a pass.

`radar scan` reports a producer change against another branch only when that
branch changed the binding or its consumer file; branches that leave the
consumer untouched are covered by the producer branch's own finding. A
removed property is reported once (`field_removed`), not also as a lost
required guarantee.

`radar affected` follows inferred file dependencies backwards from changed
files (in both the base and head graphs) and maps them to contracts and plan
tasks. `radar mcp` exposes read-mostly commands as MCP tools; test execution and
review declarations are not exposed. `radar_index` returns counts and
diagnostics (`index --summary`) and tool output is capped at 40,000 bytes.

Native-host release packaging includes dependency notices and checksums;
a manual artifact preparation workflow and checksum installer are available; no release was published by this milestone. Signing is not configured. Initial execution evidence
covers linux/amd64. Windows/macOS builds and release binaries are not yet
validated. Indexing retains transactional full snapshots; incremental parsing
and source-specific semantic reference resolution are deferred.

Compatibility direction is explicit: request schemas constrain what consumers
send, response schemas what they receive. Request widening (integer→number,
added nullability, added enum values) and response narrowing are compatible.
Loss of a required response guarantee, a newly nullable response field or a
removed request enum value are breaking. Response enum additions and request
property removals are risks. Missing direction makes every change a risk.
Strict approved-design drift still reports unplanned changes even when
schema-compatible.

## Agent-native integration milestone

- `setup --agent codex|claude|both [--dry-run] [--hook]`: project-local embedded skills, MCP and optional Claude Stop hook. Existing conflicting Radar configuration is refused; unrelated settings remain. Agent trust/restart and placing radar on PATH may require manual action.
- `check --base REF [--head REF|WORKTREE] [--plan PATH] [--require-complete]`: changed files, inferred reverse dependencies, explicit contract comparison, static discovered candidate comparison and optional plan criteria. No initialization or manifest required; absent coverage is incomplete/unknown. No code executes.
- `discover [--ref REF]`: inspect static candidate relationships and a proposed manifest. Discovery never accepts bindings automatically and never establishes runtime compatibility.
- `merge-check --base REF --branches REF,REF`: Git's combined candidate in private state, textual conflicts, contract and dependency analysis. `--verify --allow-execution -- COMMAND ARGS` observes the supplied command; `--suite recommended` explicitly selects proposed commands. Missing tools/results remain environment errors/unknown, branch test results cannot establish combined success. Exact candidate evidence is separate from historical plan evidence.
- MCP offers read-only equivalents. Bounded agent repair guidance uses finding IDs, evidence, source locations, remediation and required revalidation. Planning warns about unordered component ownership and missing shared integration criteria; shared criteria do not establish test quality.

Initial preview rejects symlink/submodule trees instead of implementing their trust and materialization semantics. Branch order is explicit; merge order can change the candidate. Analysis coverage remains incomplete even when the authorized tests pass. Test command recommendations are now available as static proposals; execution remains explicitly authorized. There is no automatic dependency installation, autonomous retry loop or secure execution sandbox.


## Selected verification gates and test recommendations

`check` and `merge-check` separate `gate.verdict` from legacy aggregate `status`
and analysis `coverage`. `--policy PATH` selects required checks; required
unknown/incomplete evidence is blocked (or failed when `on_missing: fail`), while
other analyzer limitations stay visible. Legacy exits and `--require-complete`
remain compatible without policy. See [policy semantics](VERIFICATION_POLICY.md).

`--suggest-tests` inventories conventional Go, Python unittest/pytest, Node test,
Jest/Vitest and Cargo files/configuration without loading repository code or
executing package-manager scripts. Recommendations carry argv, repository-relative
CWD, priority, affected files and inferred/proposed reasons. Plan `test_run`
commands rank ahead of direct/inferred dependencies and package fallbacks;
integration-named suites may be prioritized for multiple affected package roots.
Naming and static imports do not establish behavioral coverage. Unknown JS
frameworks and TypeScript Node tests without a recognized transpilation path
remain unselected. Tool availability is a PATH probe, not a dependency check.

`merge-check --verify --suite recommended --allow-execution` runs selected
commands on the actual combined candidate, recording each selection and execution
separately with its CWD. At most 16 commands may execute, under a shared total
`--timeout` (default two minutes, maximum 30 minutes). Larger suites are rejected
for manual narrowing. No supported selected tests means unknown, never passed.
Missing tools/dependencies, setup failures, or zero recognized results cannot
become successful test evidence. Discovery is bounded to 10,000 eligible files,
1 MiB per read and 32 MiB total; skipped inputs appear in inventory diagnostics.


MCP `radar_check` and `radar_merge_check` accept read-only `policy`,
`suggest_tests`, and `detail`. Default summaries include up to ten findings and
eight recommended commands and omit inventory. `detail: true` requests the full
report, still subject to the 40,000-byte tool cap; complete reports are available
through CLI JSON. Execution and review arguments remain prohibited.


Reviewed plan candidate verification accepts exact `test_run` argv/configuration
and optional `cwd` with setup/env/JUnit, bound to the reviewed digest and ordered
combined checkpoint. It rejects host dependency links and stale pre-existing
JUnit reports. Candidate source mutation invalidates suite test eligibility;
branch evidence and unrelated ad hoc observations cannot satisfy criteria.
Policies can require `plan_verification` and `criterion:<id>`. Existing schema-1
records remain readable; candidate metadata is additive. See
[intelligent verification](INTELLIGENT_VERIFICATION.md) for limits and the actual
regression report before making a deployment-readiness claim.


Explicit combined verification supports `--cwd DIR` (default repository root).
Nondefault CWD requires an explicit command and cannot override recommended-suite
command directories. Candidate plan `env` declarations require the named values
to exist, but only variable names are retained in provenance; this is not an
environment-value reproducibility claim. Legacy ordinary test execution retains
its optional environment pass-through behavior.


A passing candidate criterion can coexist with an unknown full plan, for example
when baseline/implementation contract scope is undeclared. Selecting
`criterion:<id>` does not silently satisfy `plan_verification` or establish
comprehensive architecture intent.


Test recommendation also follows existing statically declared `EXPOSES` and
`CONSUMES` contract relationships and supported contract/schema-field `DEFINES`
links, combined with reverse imports. Producer model or schema changes can reach
declared consumers and their tests with `declared_contract_impact` and manifest
locations. Proposed, inferred or unknown contract links are excluded; unrelated
contracts retain separate identity. Relevance remains inferred and runtime use
is unproved. Repositories without bindings still receive import/package-based
recommendations, without invented cross-language transport links.
