# Architecture

Radar is one Go binary: a CLI over a set of analysis packages, with SQLite for
local state. There is no daemon, no network service and no LLM call in the
analysis path.

## How `radar gate` works

1. **Pick inputs** (`internal/cli/gate.go`). The base is the branch behind
   `origin/HEAD`, else `main`, `master` or `trunk`. Branches are every worktree
   branch with commits beyond it, unless you name them. Only commits count;
   dirty files are reported and left out. In a workspace,
   `workspace_gate.go` repeats this per repository.
2. **Build the candidate** (`internal/integration/candidate.go`). Objects are
   copied into a temporary repository and merged there with
   `merge --no-ff --no-verify`, with hooks and external configuration
   disabled. The user's refs, index and worktree are never written. Shallow
   clones share their history boundary with the candidate.
3. **Analyze** (`integration.Analyze`). Textual merge, declared contracts,
   migration order (`internal/migrations`, only when a migration history
   changed), direct lockfile/uniqueness rules, discovered candidates and declared
   generated-output relationships, a workspace-aware candidate index, supported
   symbol conflicts, reverse import impact, then test selection.
4. **Run tests** (only with `--run`; `integration/execute.go` and
   `internal/evidence`). Selected commands run in their declared directory
   with a timeout, output limit and reduced environment.
5. **Decide** (`integration/gate.go` and `internal/gate`). The policy names the
   required checks. The verdict is computed from those checks only.
6. **Render** (`gate.go`, `render.go`, `workspace_gate_render.go`). Color is
   decoration; marks and words carry the meaning. JSON and MCP output are
   never styled.

## Packages

| Package | Responsibility |
| --- | --- |
| `cmd/radar` | entry point only: interactive menu on a terminal, `cli.Run` otherwise |
| `cli` | command table, flags, human and JSON rendering, MCP server (reuses the command handlers) |
| `indexer` | the one parse loop (`indexer.Build`) for both working tree and commits; import resolution |
| `languages` | Tree-sitter adapters for TS/JS, Python, Go, Rust, Java, C, C++ |
| `checkpoint` | indexes commits straight from Git objects |
| `contracts`, `contractgraph` | schema comparison and declared contract edges |
| `discovery` | static contract candidates |
| `migrations` | supported migration graph/version histories and explicit locations across base, single-branch merges and candidate |
| `symbols`, `lockfiles`, `uniqueness` | supported static conflict scopes, direct dependency locks and literal/declared key uniqueness |
| `generated`, `runners`, `coverage` | declared output relationships, explicit runner profiles/dependency cache and fresh command-suite file observations |
| `integration` | private candidate, analysis, execution, gate input |
| `landing`, `overlap` | pair matrix/order, bounded conflict briefs/private refs, saved-state drift |
| `runproof`, `scip` | immutable historical reports, explicitly bound external occurrences |
| `composition`, `workspace` | multi-repository scope, registry, team file, run records |
| `testselection` | test inventory, selection modes, grouping, budgets |
| `evidence` | runs commands and classifies results |
| `gate` | evaluates required checks against a policy |
| `planning`, `verification` | plans, task DAGs, rule checks |
| `graph`, `model`, `storage` | graph queries, shared value types, SQLite |
| `git`, `pathutil`, `jsonptr`, `project`, `termui`, `onboarding` | Git access, path validation, JSON pointers, state location, terminal styling, `radar setup` |

Analysis packages never import `cli`. Git subprocesses live in `git`, process
execution in `evidence`, and analysis rules in the package that owns them.

Native agent Stop adapters (`cli/agent_hook.go`) reuse the watch input observer
and private dirty-candidate analysis for one static preview. Onboarding writes
project-local hook configuration; portable plugin packages supply the same
command. They do not execute tests or control agent permissions. Their result
cache binds local/peer saved state and analysis configuration. A separate bounded
syntax cache reuses parse results by path/content/Go build identity; manifests,
import resolution and source safety checks run again for each tree. Loaded
entries retain source-byte costs and LRU eviction leaves room for fresh syntax.
Builds without an available linker ID disable cross-process cache reuse.

Landing reuses `integration.Analyze` for pairs and reanalyzes the complete
ordered candidate before publishing a private ref. Pair results cannot authorize
the N-way candidate. Conflict briefs carry bounded committed excerpts and inferred
test associations. External structural drivers require explicit CLI execution
consent; MCP exposes static planning only. Cross-worktree drift applies existing
static detectors to saved snapshots and remains a repair lead.

## Evidence model

Every node and edge records how it was found: `verified_static`,
`verified_tool`, `observed_test`, `inferred`, `proposed` or `unknown`.
Tree-sitter output is syntax. Import edges are `inferred`. Plan content stays
`proposed` even after review. One category is never promoted to another.

Entity IDs are a kind, a repository-relative path and a qualified name
(`function:svc/server.go#Server.Handle`). Repositories are identified by their
root commits. Absolute paths never appear in IDs or evidence, so plans and
records stay valid across clones and worktrees.

Test evidence is bound to the repository, the exact commit or candidate tree,
the plan digest, the argv and directory, and the criterion. Records are
insert-only. Records from a branch never count as evidence for a combined
candidate. Historical `gate --trailer` reports are stored separately by
`runproof`; `show` checks identity/digest and replays the recorded candidate,
without claiming current validity or signer authentication.

## Design decisions

- **SQLite with atomic snapshots.** Each index is written in one transaction,
  so an interrupted run never leaves half a graph. The newest ten working-tree
  snapshots per repository are kept; commit snapshots are kept because plans
  pin them. Uses the pure-Go modernc driver.
- **Tree-sitter, built in.** Grammars ship in the binary, which needs CGO at
  build time but no language toolchain at runtime. Explicit SCIP input can add
  producer-asserted `verified_tool` occurrences when revision/index/source
  hashes match. Radar does not run the producer or authenticate it. Native
  go/types, rust-analyzer and tsc integrations remain unavailable.
- **`ParseWithOptions` instead of `ParseCtx`.** The pinned upstream `ParseCtx`
  can race parser teardown. Adapters use a bounded input callback and a parser
  timeout with context checks instead.
- **Declared contracts before inferred ones.** Only bindings in
  `.radar/contracts.json` can fail a gate. Discovery proposes; a person
  accepts.
- **Unsupported schema constructs are compared as opaque subtrees.** An
  unrelated `format` keyword used to hide a removed property, so annotations
  no longer abort a comparison. Changed constructs Radar cannot reason about
  make the report `incomplete`.
- **Environment errors are not test failures.** A command that fails before any
  recognized result is `error`. The exception is a compile error in repository
  source, which is a real failure of the combined code.
- **No consequential actions for agents.** MCP leaves out `test` and
  `approve`. Reviews are declarations bound to a plan digest, and editing the
  plan invalidates them.
- **Conservative scheduling.** Tasks that share a component or contract are
  never marked parallel.
- **Native release builds.** CGO makes cross-compiling fragile, so each target
  is built and tested on its own runner.

## Licensing

Radar is MIT. Tree-sitter and the bundled grammars are MIT, the modernc SQLite
driver is BSD-3-Clause and SQLite is public domain. Release packaging collects
notices for every linked module.
