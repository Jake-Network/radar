# Initial technical decisions

1. **Go modular monolith.** The CLI orchestrates graph, indexing, planning,
   contract analysis, and storage packages without a service dependency.
2. **SQLite with migrations and atomic snapshots.** Keep repository state
   local and transactional. An interrupted index must not publish half a graph.
3. **Tree-sitter for all initial structural adapters.** Upstream grammars avoid
   ad hoc source parsing and ship in the binary. CGO requires a C compiler for
   source builds. Semantic compiler or SCIP adapters are deferred and reported
   unavailable. SCIP describes symbols/references, while Tree-sitter describes
   syntax; one cannot silently substitute for the other.
4. **Explicit contracts before inferred network relationships.** JSON schema
   comparison and declarative consumer field bindings create a reproducible
   cross-language slice without pretending to trace arbitrary HTTP calls.
5. **Proposed graph separate from observed graph.** Plan deltas preserve proposed
   evidence. Static relationships, review declarations, and observed behavior
   are distinct; no LLM result becomes verified evidence automatically.
6. **Narrow deterministic verification.** Presence rules run safely without
   executing repository code. Behavioral tests require explicit command evidence; scalability and durability
   remain unknown unless appropriate tests are declared and executed.
7. **MIT project licensing.** Tree-sitter and its chosen grammars use MIT;
   modernc SQLite uses BSD-3-Clause, and underlying SQLite is public domain.
   Preserve upstream notices and audit dependencies when packaging binaries.

The first release targets trustworthy small vertical slices. Incremental
semantic indexes, authenticated approvals, automatic agent orchestration, and
comprehensive schema compatibility are deferred.

8. **Content-derived working-tree checkpoints.** CLI indexing hashes source
   content and indexed contract evidence; preflight re-analyzes current source
   to reject stale plans. Authoritative branch comparisons pin refs to commit
   SHAs. Commit-pinned implementation verification is authoritative only with a valid
    digest-bound review declaration; current working-tree verification is informational.
9. **Joined parser lifetime without cancellation goroutines.** The pinned
   upstream `ParseCtx` implementation can race parser teardown. Adapters use
   bounded `ParseWithOptions` input callbacks and a parser timeout, with context
   checks, rather than the deprecated asynchronous cancellation wrapper.
10. **Conservative task scheduling.** A shared declared component or contract
    prevents parallel eligibility. Unordered contract owners are preflight
    errors. Task packets carry requirement/acceptance evidence and plan digest;
    scheduling does not prove independent runtime behavior.

Review declarations are optional for routine tasks and required for explicitly
consequential tasks. `radar tasks --plan candidate.json --json` emits
`plan_digest`; after actual review, record reviewer, RFC3339 `reviewed_at`, the
plan's `base_revision` as `checkpoint`, and that digest in `approval`. Editing the
plan invalidates its review. Radar does not manufacture or authenticate reviews.

11. **Local proof records, no automatic code execution.** `test` requires explicit
    opt-in, a declared argument array and a committed source snapshot. Verification
    consumes SQLite evidence bound to the exact commit and plan. A zero exit code
    proves only command success; incomplete test coverage remains a user concern.
12. **Declarative review rather than invented approval.** `approve` writes a new
    artifact with the supplied reviewer identity, baseline and plan digest after
    the user performs a review. It does not establish authenticated human approval.
13. **Host-native release packaging.** CGO grammars make transparent native builds
    simpler than unverified cross-compilation. Packaging collects notices for the
    complete linked Go module set, a dependency inventory and archive checksum.

Authoritative implementation checkpoints must descend from the approved baseline. Test evidence attachment is operational metadata excluded from the design digest; exact command, criterion identity and the complete design remain bound. Standalone task packets include approved contract projections and proposed graph deltas. Duplicate projections for one contract are rejected.

14. **Location-independent identities.** Entity IDs are `kind:path#Qualified.Name`
    and evidence names the repository by its root commits. Absolute checkout
    paths made plans, task packets and test evidence invalid in other clones and
    in the Git worktrees parallel agents use. Readable IDs also let agents and
    reviewers write plans without copying hashes.
15. **One indexing loop, two providers.** Working-tree and committed indexing
    share `indexer.Build`; the working tree honors `.gitignore`, commits stream
    blobs through `git cat-file --batch` instead of a subprocess per file.
16. **Inferred import dependencies.** Path-convention resolution gives file-level
    impact analysis without compilers. Edges are labeled `inferred` and never
    substitute for semantic references.
17. **Local contract comparison.** Annotation and validation keywords no longer
    abort a binding: an unrelated `format` previously hid a removed property.
    Unsupported constructs are compared as opaque subtrees and reported as
    unanalyzed only when they change; reports carry `incomplete` status.
18. **Test environment errors are not test failures.** A command that fails
    before any recognized result is `error`. Local dependency caches are reused
    read-mostly offline; further environment needs are declared in the reviewed
    rule (`setup`, `env`, `link`, `junit`). Output tails are shown, never stored.
19. **Assumption lifecycle.** Assumptions are open, accepted or resolved with a
    resolution, so plans can pass without deleting unresolved premises.
20. **Agent surfaces exclude consequential actions.** The MCP server exposes
    read-mostly commands; `test` (executes code) and `approve` (declares a human
    review) stay with the user. The Claude Code Stop hook blocks only on
    supported failed checks.
