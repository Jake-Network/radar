---
name: radar-architecture
description: Ground feature design and verification in Radar repository evidence.
---

# Radar architecture workflow

Run the local `radar` binary from the target repository. If it is unavailable,
report that fact; do not fabricate Radar output.

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
   `rule.command` (plus `setup`, `env`, `link` or `junit` when needed). Review
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

Start a repository without manual bindings with `radar check --base BASE --json`.
Use `radar discover --json` for evidence-backed proposed relationships; review
uncertainties before explicitly accepting any manifest. Lexical similarity is
not runtime proof. MCP equivalents are `radar_check` and `radar_contracts_discover`.

Before integrating concurrent branches, run
`radar merge-check --base BASE --branches A,B --json` (or `radar_merge_check`).
This previews combined source using Git semantics. It does not merge user branches
or execute tests. Independent branch tests are never evidence for the combination.
After explicit execution authorization, use the CLI with `--verify --allow-execution`
and a plan declaring exact test commands. Filesystem isolation is not a secure sandbox.

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
