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

Git operations are read-only with bounded subprocess contexts. Committed refs are stable checkpoints; working-tree observations are informational. Verification checks approved deltas and supported explicit rules, preserving unknown when coverage is absent.

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
bounded, read-only revision access. `project` locates state: a linked worktree
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
not launch agents, send external messages or apply Git integration operations.

`planning.IntentGraph` connects requirements, decisions, constraints, tasks, acceptance and verification rules to indexed components and contracts with proposed relationships. CLI `graph --plan` exports this view; `--projected` applies explicit graph deltas. Static source provenance remains separate. Finding identities include repository, analyzed revision and plan digest so later reports cannot replace earlier branch evidence under the same ID.
