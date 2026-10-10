# Architecture

A modular Go monolith: CLI → indexer/language adapters, planning/preflight, contracts/Git comparison, verification → provenance-bearing graph → SQLite. Models are shared value types; storage owns transactions. No daemon or distributed dependencies.

## Evidence model

Nodes have readable identities derived from kind, repository-relative path and
qualified scope (`function:svc/server.go#Server.Handle`), never from the
checkout location, plus revision-specific provenance. Edges record analysis method, repository, revision, file/line, timestamp and evidence category. Categories: verified_static, verified_tool, observed_test, inferred, proposed, unknown. Import-path file dependencies (`DEPENDS_ON`) are inferred. Graph identity includes location/scope rather than display name alone. Current snapshots and explicit proposed deltas are separate.

## Parsing and semantic boundaries

Use upstream Tree-sitter Go bindings and maintained TS/JS/Python/Go/Rust grammars, all permissively licensed. Embedded syntax parsing requires CGO and a C compiler at build time, but no external language executable at runtime. Tree-sitter parses syntax; SCIP carries compiler/indexer symbol identity and references. SCIP ingestion, compiler-resolved calls, TypeScript type checking, Python type resolution, go/types and rust-analyzer integration are deferred, and must never silently degrade into confirmed guesses.

References: [upstream Go bindings](https://github.com/tree-sitter/go-tree-sitter), [SCIP documentation](https://sourcegraph.com/docs/code-navigation/writing-an-indexer), [SQLite driver](https://github.com/modernc-org/sqlite). SQLite uses database/sql, versioned migrations and transactional snapshot replacement. Snapshots are stored as one payload per revision; only the newest ten working-tree snapshots per repository are retained, while commit snapshots are kept because plans pin them. A busy timeout serializes competing writers; readers see complete snapshots.

## Contracts and planning

Explicit JSON OpenAPI/JSON Schema documents and consumer bindings provide a reproducible cross-language boundary. Compatibility findings are directional and bounded to implemented checks, never arbitrary HTTP tracing. Plans carry requirements, decisions, constraints, deltas, tasks, dependencies, acceptance and verification. A DAG scheduler rejects missing dependencies, cycles and concurrent ownership overlap. Agent reasoning supplies unresolved design choices; deterministic planning only creates evidence context and incomplete fields.

The shared Git inspection package is read-only with bounded subprocess contexts. Integration preview performs Git merges only in a private repository with copied objects, never shared writable Git state. Committed refs are stable checkpoints; working-tree observations are informational. Verification checks approved deltas and supported explicit rules, preserving unknown when coverage is absent.

Preflight analyzes current content to detect stale baselines. Graph filters return closed subgraphs, including only edges whose endpoints are selected. Missing entities in incomplete indexes remain unknown rather than proving removal or failure.

## Indexing pipeline

`indexer.Build` is the single parse loop. A `Provider` supplies entries and
content: the working-tree provider lists files with `git ls-files` (honoring
`.gitignore`, falling back to a directory walk outside Git); the commit
provider lists the tree and streams blobs through one `git cat-file --batch`
process. Both share limits, exclusions and diagnostics, so working-tree and
committed snapshots of the same content are identical apart from revision.
After parsing, a resolver maps import specifiers to repository files by
language convention (relative TS/JS paths, Python packages, `go.mod` modules,
Rust `crate`/`self`/`super`) and adds inferred `DEPENDS_ON` edges. Git listings
use a large output bound separate from single-file reads.

## Module boundaries and checkpoint workflow

`cmd/radar` is a small executable entry point. `internal/cli` owns a command
table (each command declares only its flags), human and JSON rendering, and the
MCP server, which re-enters the same command handlers; analysis packages do not
depend on it. `pathutil` validates repository-relative paths and `jsonptr`
implements JSON pointers for every package. `indexer` coordinates
`languages`, `contracts` and `graph`; `planning` owns plan values, DAGs and context;
`verification` checks observed implementations; `evidence` owns opt-in subprocess
execution; `storage` owns SQLite schema migration and transactions. `git` provides
bounded, read-only revision access. `termui` decides whether human output may be styled (terminal detection,
`--color`, `NO_COLOR`/`FORCE_COLOR`, Windows console setup); styling lives only in the CLI
renderers and never reaches JSON or MCP output. `project` locates state: a linked worktree
without its own `.radar` uses the main worktree's state. Keep new rules in analysis packages and CLI
presentation in the CLI rather than growing the entry point.

Design baseline and implementation SHA are separate. Review declarations bind a
plan digest to its baseline. Verification reads implementation files from the
selected Git commit and compares explicit deltas to the baseline. Test records
bind repository identity (root commits, shared by clones and worktrees),
implementation commit, plan digest, declared command, criteria, harness counts,
timestamps, exit status and output digest. Integrity hashes detect
accidental changes; they do not authenticate a local database against its owner.
Tests execute in private committed snapshots only after explicit opt-in. SQLite
persists records; verification consumes record IDs rather than trusting a supplied
"passed" assertion. Missing or mismatched records yield unknown evidence.

`affected` reverses `DEPENDS_ON` reachability from changed files in the base
and head graphs and maps the result to contract bindings and plan tasks.

Task reports and structured feedback are coordination artifacts, not a process
controller. Contract ownership and DAG eligibility guide parallel work; Radar does
not launch agents, send external messages or modify user branches.

`planning.IntentGraph` connects requirements, decisions, constraints, tasks, acceptance and verification rules to indexed components and contracts with proposed relationships. CLI `graph --plan` exports this view; `--projected` applies explicit graph deltas. Static source provenance remains separate. Finding identities include repository, analyzed revision and plan digest so later reports cannot replace earlier branch evidence under the same ID.

## Onboarding, discovery and combined verification

`onboarding` embeds agent assets in the binary and prepares project-local configuration changes before writing. Dry runs and conflict detection preserve existing user settings. Setup never writes home configuration.

`discovery` reads bounded regular files, uses Tree-sitter declaration/member evidence alongside narrow static patterns, and proposes endpoint/model/imported-type/field candidates. Provenance separates observed syntax from inferred transport relationships. Proposed manifests are output for review, not persisted as accepted contracts.

`integration` resolves ordered input commits, transfers objects into a private Git repository, applies Git merge semantics there and analyzes the exact combined candidate. It rejects unsafe paths, symlinks and submodules. Repository hooks and external Git configuration are disabled. Execution is explicit opt-in with host permissions, not an OS sandbox. Integration evidence records input revisions, candidate commit/tree, command and optional plan digest, with output digests rather than raw output. Branch evidence is never consumed as integrated evidence. Temporary candidate state is removed on return. Default preview and verification write no files into the original repository; `--evidence-output .radar/evidence/candidate.json` explicitly saves structured metadata to a new path.

`check` composes affected-file analysis, explicit and discovered contract comparison, manifest lint and optional plan verification. Each coverage check retains its own status. Unknown architecture, semantic or execution properties cannot be promoted by a successful Git merge or empty finding list. Read-only MCP exposes check/discovery/merge preview and excludes execution authorization and review declarations.


## Selected evidence and test inventory

`internal/gate` evaluates required named checks independently of analyzer coverage.
Policies are versioned data and cannot authorize execution. CLI presentation keeps
the existing aggregate status for compatibility and adds selected gate verdicts
and structured coverage; explicit policy chooses the corresponding CI exit.

`internal/testselection` reads bounded source-state providers for conventional
tests and ecosystem manifests without importing code or evaluating configuration.
The same inventory path serves working-tree and committed source. Selection
combines plan-declared argv, reverse inferred import reachability, package-local
companions and conservative package/integration fallbacks. Reasons retain their
inferred/proposed classification. It does not claim test completeness.

The integration layer observes each recommended command in its declared CWD and
binds its selection ID, argv, result and source state into candidate evidence.
A shared execution deadline and a command budget (default 16, after grouping;
see [TEST_SELECTION.md](TEST_SELECTION.md)) bound each invocation; omitted
required commands are reported and block the gate. There is no retry controller or dependency installation is introduced. Recommendation,
execution authorization and observed results are distinct operations.


Candidate plan observations use additive fields on existing schema-1 evidence:
`candidate` checkpoints bind repository/base, ordered inputs, candidate commit/tree,
source hashes, reviewed execution configuration and review digest. Exact reviewed
argv/CWD run with declared setup/env/JUnit inside private source; host dependency
links and pre-existing JUnit reports are rejected. The integration layer evaluates
plan rules after execution and accepts only exact unchanged-source candidate
records, never branch records or ad hoc observations. `plan_verification` and
`criterion:<id>` may be selected gate requirements. Integration evidence output
preserves single-command objects and adds ordered suite arrays with optional
`plan_record`; neither format authenticates its owner or malicious repository code.


The explicit-command CLI `--cwd` is validated as a candidate-relative directory;
recommended suites retain each selected CWD. Candidate plan environment names
must exist, and evidence retains only those names rather than their values.
Source/configuration integrity does not authenticate environment values or
establish hermetic execution. Legacy ordinary evidence behavior remains compatible.


`testselection` combines reverse import impact with `verified_static` explicit
`EXPOSES`/`CONSUMES` edges and supported contract/schema-field `DEFINES` plus
schema source provenance. Contract identity is retained across traversal so
unrelated declarations do not join through lexical similarity. Selection reasons
carry `declared_contract_impact` and manifest locations, but relevance remains
inferred. Discovered/proposed/unknown contract links are deliberately excluded;
manifest-free selection does not establish cross-language runtime transport.
