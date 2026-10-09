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
   of runtime behavior or authorization.
2. Fill the generated versioned plan: requirements, alternatives and tradeoffs,
   constraints, proposed contract/graph deltas, proof-carrying tasks, dependencies,
   acceptance rules, and unresolved assumptions. Keep uncertain claims proposed
   or unknown. Use `.radar/contracts.json` only for legitimate declared links.
3. Run `radar preflight --plan <path> --ref <baseline SHA> --json` and `radar tasks --plan <path>`.
   Correct grounded inconsistencies. Present unresolved consequential design
   decisions for review; obey the current user's existing authorization.
4. After actual review, use `radar approve --plan <path> --reviewer <identity>
   --output <new approved path> --json` to record the local declaration. Never
   invent reviewer approval or supply a human identity on their behalf. Implement independent tasks
   according to dependencies and ownership. This skill does not authorize Git
   merges, rebases, resets, pushes, or external messages.
5. At committed checkpoints run `radar impact --base <ref> --head <ref>` or
   `radar scan --base <ref> --branches <ref1>,<ref2>`. Working-tree observations
   are informational, not final failures. Update declared consumer dependencies
   only after the corresponding migration, not to hide a finding.
6. For behavioral criteria declare exact argv in `rule.kind: test_run` and
   `rule.command`. Review repository code before opting in with
   `radar test --plan <approved path> --ref <implementation SHA> --allow-execution
   --timeout 60s --json -- <declared command argv>`. Private snapshots protect the
   checkout but are not a security sandbox. Collect the returned evidence IDs.
7. Run `radar verify --plan <approved path> --ref <implementation SHA>
   --evidence <ID,ID> --json`. Read drift findings, per-task status and feedback.
   Report passed, failed, warning and unknown evidence accurately. A successful
   command proves its execution outcome, not comprehensive requirement coverage.
   Missing evidence and unsupported checks remain unknown; do not hide them by
   changing declarations or deleting criteria.
