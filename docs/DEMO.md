# Reproducible local demo

Build with Go 1.23+, CGO enabled, and a C compiler. This script also needs Git,
Bash, and Python 3. It copies the fixture into a fresh temporary Git repository;
all branch creation and commits are confined to that copy. Read the script
before explicitly running it.

```sh
go build -o /tmp/radar ./cmd/radar
bash examples/organization/demo.sh /tmp/radar
```

The script prints actual JSON results and retains its temporary repository.
It checks these scenarios:

1. Index real TypeScript/JavaScript, Python, Go, and Rust declarations and imports.
   Query `ExportSummary` in the persisted graph.
2. Preflight an export plan and an intentionally inconsistent plan. The second
   has unordered shared-contract writers and lacks security verification.
   Both retain unresolved authorization and scalability assumptions.
3. Verify the baseline's `total` property and authorization file presence.
   These checks are intentionally narrow; runtime authorization is unverified.
4. Rename `total` to `total_cents` in a producer branch's explicit schema while
   an independent consumer branch retains its declared `total` dependency.
   Scan committed branches and report the schema removal and declared consumer.
   No fixture-specific keyword detector is involved.
5. Analyze the same schema edit before committing. `WORKTREE` observations are
   informational warnings, not authoritative integration failures.
6. Verify the producer checkpoint and detect the missing accepted `total` field.
7. Scan independent Python and TypeScript changes with unchanged contracts and
   observe no contract incompatibility findings.

`plans/exports.json` is a minimal design candidate, not an approved production
architecture. The script binds the plan template to the index's actual committed
baseline; verification without a review declaration remains informational. It also runs
`radar plan` to produce an incomplete grounded bundle for agent-assisted design.
The sample queue is in memory; isolation, durability, cancellation, retries, and
large-data performance require additional behavioral evidence. To use a real
plan, pin its indexed revision and record a digest-bound checkpoint review.

## Committed implementation and test evidence

```sh
bash examples/verification/demo.sh /tmp/radar
```

This separate temporary repository creates a baseline, an additive schema plan,
and a local review declaration named `fixture-declaration` (a test identity, not
an assertion of real human approval). It commits the compliant implementation,
executes one real Python unittest through `radar test --allow-execution`, and
checks that `verify --ref SHA --evidence ID` reports an authoritative pass. It
then commits a producer that removes the accepted `total` property and runs the
same unittest to produce a real failure. Verification distinguishes the failure
and rejects the previous passing evidence because it belongs to another commit.
An amended plan is also checked: its old review declaration and evidence cannot
verify the new plan digest.
The fixture is retained for inspection, including SQLite records and JSON reports.

The plan declares an exact target schema object for its contract delta and exact
argv for its `test_run` criterion. The successful command supports the specified
criterion only. It does not prove security, durability, or all response behavior.
Private test snapshots are not OS sandboxes. These scripts create only disposable
fixture commits and never mutate the Radar checkout's Git history.
