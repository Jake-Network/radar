---
name: radar-architecture
description: Check that parallel agent branches integrate before merging (radar gate), and ground feature design and verification in Radar repository evidence.
---

# Radar architecture workflow

Run the local `radar` binary from the target repository. If it is unavailable,
report that fact; do not fabricate Radar output.

Before merging parallel work (several agents, worktrees or branches), run
`radar gate --json`. With no arguments it combines every worktree branch that
has commits beyond the base in private Git state and reports conflicts,
breaking contract changes and, in `attribution`, the branches whose files each
finding points at. It never runs repository code. Follow its `next` field;
`radar gate --run` executes repository tests on the combined tree, so ask the
user first. Repair on the attributed branch, commit and rerun; results for
individual branches never prove the combination works. After `--run`, an
`attribution` whose `basis` starts with `observed` comes from rerunning the
failed test on fewer branches; `inferred` comes from imports. Gate and watch
default to zero optional subset diagnostic reruns. Failed commands automatically
receive one budgeted base comparison; base-existing cases are not branch leads,
and all-base failures leave verification incomplete. Inspect `base_comparison`
without treating its base execution as candidate proof. After execution is authorized, opt in with
`--run --attribution-runs 8` when diagnostic subset observations are needed.

Reading the result: `gate.verdict` is `pass`, `fail`, `blocked` or `error`
(exit 0, 1, 1, 2). `PASS (static)` means no test ran; say so when you hand
off. A required check is satisfied only by `passed`. `blocked` means
required evidence is missing (including a documentation-only change, for
which no test ran): report `next` and the gaps in `selection.blocking` and
`uncovered_changes`; never call it a pass.
Gate lists uncommitted changes in `worktrees` but checks committed inputs only.
For continuous previews use CLI `radar watch --include-dirty --jsonl`: it includes
saved staged, unstaged and nonignored untracked changes in selected worktrees.
Unsaved buffers and ignored files are excluded. Add `--run` only with explicit
execution authorization; it repeatedly executes with host permissions. Dirty
preview evidence does not prove the committed branches passed. In JSONL,
`changed` or `error` invalidates the current verdict; correlate `completed`
results with `state.key`. `verification_state` is current; `last_completed` and
`last_change` are event timestamps. Heartbeats contain no report; optional
`attributed` events leave the Fast Gate verdict unchanged.

`radar doctor --json` shows the base, branches gate will combine and whether test
runner programs are on PATH (`project`). `radar help agents` prints this contract
in short.

In a workspace (`radar workspace add PATH` groups repositories on this
machine), the same command checks every workspace repository, each combined on
its own. Name branches in several repositories as `radar gate
orders:agent/api payments:agent/client`; unnamed repositories take part at
their base. Declared workspace links compare producer schemas with consumer
fields across base/candidate combinations. Only `cross_repo.status: passed`
is evidence that the declared, supported static links passed; per-repository
passes alone do not establish cross-repository compatibility. Check `links`
for incomplete inputs and intermediate-cell failures. Runtime behavior and
rollout safety remain unverified by these static checks. `suggested_links` are
proposals and never affect the verdict. To declare one, use the CLI
`radar workspace connect PRODUCER CONSUMER_REPO --fields a,b --direction response`
(with `request` for request contracts and optional `--source PATH` / `--into repo:PATH`),
then commit the home repository's `.radar/workspace.json` and the consumer's
`.radar/consumes.json`. Gate reads the committed home base declaration; replay
pins the recorded base and team blob. `.radar/contracts.json` describes links
within a repository; workspace links cross repositories. MCP does not expose
`connect`.

For feature design and verification:

1. Select a committed baseline. Run `radar doctor`, `radar index --ref <SHA> --json`,
   and `radar plan "feature intent" --ref <SHA>`.
   Read the generated context and diagnostics. Investigate source locations for
   the feature before proposing architecture. Structural indexing is not proof
   of runtime behavior or authorization. `radar resolve QUERY` turns names or
   `path#Qualified.Name` into entity IDs; `radar affected --base <SHA>` and
   `radar graph --from file:<path> --edge DEPENDS_ON --reverse` show dependents
   through inferred import paths.
2. Fill the generated versioned plan: requirements, alternatives and tradeoffs,
   constraints, proposed contract/graph deltas, proof-carrying tasks, dependencies,
   acceptance rules, and assumptions (`{"id","text","status","resolution"}`;
   keep them `open` until investigated, never delete them to pass checks).
   Keep uncertain claims proposed or unknown. Use `.radar/contracts.json` only
   for legitimate declared links; `radar contracts` flags stale declarations.
3. Run `radar preflight --plan <path> --ref <baseline SHA> --json` and `radar tasks --plan <path>`.
   Follow `next_steps` and correct grounded inconsistencies. Present unresolved consequential design
   decisions for review; obey the current user's existing authorization.
4. After actual review, use `radar approve --plan <path> --reviewer <identity>
   --output <new approved path> --json` to record the local declaration. Never
   invent reviewer approval or supply a human identity on their behalf. Implement independent tasks
   according to dependencies and ownership. This skill does not authorize Git
   merges, rebases, resets, pushes, or external messages.
5. At committed checkpoints run `radar impact --base <ref> --head <ref>` or
   `radar scan --base <ref> --branches <ref1>,<ref2>`. Working-tree observations
   are informational, not final failures. A report status of `incomplete`
   means some bindings were not analyzed; it is not a pass. Update declared consumer dependencies
   only after the corresponding migration, not to hide a finding.
6. For behavioral criteria declare exact argv in `rule.kind: test_run` and
   `rule.command` (plus repository-relative `cwd`, `setup`, `env`, `link` or
   `junit` when needed). Candidate integration does not support host dependency
   links; review private setup rather than linking writable checkout dependencies. Review
   repository code before opting in with
   `radar test --plan <approved path> --ref <implementation SHA> --allow-execution
   --timeout 60s --json -- <declared command argv>`. Private snapshots protect the
   checkout but are not a security sandbox. Collect the returned evidence IDs.
   Status `error` means the command failed before any test ran (dependency,
   build or setup problem), not a test failure.
7. Run `radar verify --plan <approved path> --ref <implementation SHA>
   --evidence <ID,ID> --json`. Read drift findings, per-task status and feedback.
   Report passed, failed, warning and unknown evidence accurately. A successful
   command proves its execution outcome, not comprehensive requirement coverage.
   Missing evidence and unsupported checks remain unknown; do not hide them by
   changing declarations or deleting criteria.


## Combined integration and bounded repair

Start a repository without manual bindings with `radar check --base BASE --suggest-tests --json`.
Use `radar discover --json` for evidence-backed proposed relationships; review
uncertainties before explicitly accepting any manifest. Lexical similarity is
not runtime proof. MCP equivalents are `radar_check` and `radar_contracts_discover`.
For `radar_check`/`radar_merge_check`, use `suggest_tests: true` and optional
`policy` to inspect the bounded default summary. Request `detail: true` only
when omitted findings, commands or inventory are needed; full responses are
still capped at 40,000 bytes. Use CLI JSON when the complete local report is needed.

Before integrating concurrent branches, run
`radar merge-check --base BASE --branches A,B --suggest-tests --json` (or `radar_merge_check`).
This previews combined source using Git semantics. It does not merge user branches
or execute tests. Independent branch tests are never evidence for the combination.
Inspect `verification_proposal`: argv, CWD, priority, affected files and evidence
reasons. `declared_contract_impact` can follow explicitly declared producer/
consumer links into consumer imports/tests; inspect the manifest locations.
Discovered/proposed links cannot establish that relationship, and runtime usage
remains unproved. Recommendations are inferred/proposed and do not install dependencies
or establish complete test coverage. Define a versioned `--policy PATH` selecting
required checks; distinguish `gate.verdict` from aggregate status and coverage.
Missing required evidence remains blocked; unrelated analyzer gaps stay visible.
The base `.radar/contracts.json` defines contract obligations: deleting the
manifest, a binding or a consumed field declaration never makes
`no_breaking_contracts` pass. Retire an obligation only with a reviewed
`"retired": [{"id", "fields", "reason"}]` entry (or an approved plan removal
delta); never remove declarations to silence a finding.
When a branch changes a migration history, `migration_order` is required.
Repair a reported head, leaf, label or version conflict as its `remediation`
proposes (re-point the later migration, add a merge migration or renumber a
duplicated version); never delete another branch's migration to silence it.

After explicit execution authorization, use the CLI:
`radar merge-check --base BASE --branches A,B --verify --suite targeted
--allow-execution --policy POLICY --json`. Modes: `targeted` (direct static
relationships), `balanced` (alias `recommended`; adds package fallbacks) and
`full` (whole discovered suites). Preview any mode read-only with
`radar check --suite MODE`. Supply a reviewed plan declaring exact test commands
when checking acceptance criteria. Keep selected commands separate from executed
observations; inspect every result and its CWD. Same-directory per-file commands
are grouped; `--max-commands` (default 16) and `--timeout` bound one run. Read
`selection.omitted`, `uncovered_changes` and `blocking`: an omitted, unrunnable or
unexecuted required command blocks the gate and is never a pass or a test failure.
Radar never installs dependencies without explicit declared setup and execution
opt-in. An `environment_unavailable` result needs a prepared environment,
`.radar/runners.json`, or reviewed plan `test_run` setup; combined candidates reject
host dependency links. A command observation does not automatically satisfy
an unrelated plan criterion. Private copied files are not an OS sandbox.
MCP never executes repository code or records approvals (it may write Radar
state and plan files): never pass execution flags or attempt to create approval
through a tool call. Consult Radar CLI help and its
intelligent-verification guide for the supported scope.

Consume finding IDs, evidence classes, source locations, remediation and required
verification. Repair only a supported invariant, then re-check the exact repaired
state. Stop after two unsuccessful repair attempts and report the remaining blocker.
If the same finding ID recurs without new evidence, investigate the cause before
repeating an edit; do not loop or delete criteria to suppress feedback.

For parallel work, give shared components one owner or explicit ordering. Add a
shared integration test criterion for cross-component behavior. Context includes
one-hop import and declared contract neighbors; inspect provenance and source.
Radar cannot infer contradictions in free-form design prose or establish runtime
architecture from static context. Consequential alternatives and tradeoffs still
need agent reasoning and actual human review; no MCP tool can supply that approval.

## Additional static and declared execution checks

Gate checks supported literal symbol conflicts (`no_symbol_conflicts`), changed
migration histories (`migration_order`), direct manifest/lockfile consistency
(`lockfile_consistency`) and literal or declared unique keys (`unique_keys`).
Workspace manifests improve inferred import relationships; none of these execute
repository code. Unsupported syntax stays visible as unknown. A static PASS
covers only the checks and supported scope listed in the report.

`.radar/runners.json` declares runner prefixes and setup argv; only explicit
execution opt-in can run them. Dependency cache hits are not test evidence.
Python first-party imports outside the candidate produce environment diagnostics,
not test evidence or branch leads. Follow the repair hint to create a candidate-local
environment with runner prefix/setup, or deactivate the external virtual environment.
`.radar/generated.json` declares sources, outputs and generator argv. Static
observations are warnings; --run regenerates in a separate private copy.
Contract bindings may declare `generate` argv, also execution-only. Generated
observations carry their exact checkpoint and output digest, never raw output.
Protobuf and GraphQL checks support a bounded literal subset; unsupported grammar
cannot establish compatibility. `--observe-coverage` requires execution opt-in;
fresh observed file relationships supplement static selection, and stale or
unrelated baseline records do not count as coverage.

## Landing, saved drift and retained proof

For parallel work, `radar_land` (`radar gate --landing --json`) gives a bounded
pair matrix and ancestry-aware landing estimate. It is not N-way or runtime
proof. `radar_conflict_brief` reads bounded committed hunks with inferred symbol
and related-test links; repair in your branch, then rerun the full gate.
`radar_overlap` (`radar gate --overlap --json`) inspects saved peer drift before
editing a symbol; it is a query, not a lock. No findings does not prove complete
compatibility. The Stop hook warns about inherited textual conflicts and still
checks current saved changes for repairable static failures. Static MCP tools cannot authorize merge
drivers, repository execution, or `land --apply` private-ref publication.
For an authorized `land --apply`, inspect `landing_apply.published` and its
`reason`. `objects_imported` records immutable object import, which may complete
even when no ref is published; it does not mean a user branch changed.

When the user requests a commit, a passing `radar gate --trailer` can supply the
exact `Radar-Verified:` trailer to preserve in its message. Never invent one,
attach a failed/blocked run, or imply tests ran for a static-only result.
`radar show RUN-ID` replays historical candidate evidence, not today's HEAD.
Only use `--run` when repository execution has already been authorized within
the current scope; otherwise obtain that authorization first. Preserve existing
authorization and constraints rather than asking again for the same execution.
