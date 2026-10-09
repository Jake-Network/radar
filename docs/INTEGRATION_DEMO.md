# Integration preview demonstration

```sh
go build -o /tmp/radar ./cmd/radar
bash examples/integration/demo.sh /tmp/radar
```

The deterministic fixture has a Python product API and a TypeScript checkout
configuration. One branch doubles the unit price; another doubles quantity.
Each independently passes the same budget invariant. Their files merge without
a textual conflict, but their combined result fails that invariant. Radar
observes the real unittest failure on the combined source tree, identifies the
failed test and repository source location, and verifies the repaired checkout.

```sh
radar merge-check --base main --branches feature/backend,feature/frontend
radar merge-check --base main --branches feature/backend,feature/frontend \
  --verify --allow-execution --json -- python3 -m unittest test_checkout
```

Branch ordering is explicit. Radar resolves every input to an immutable commit,
copies objects into a private Git repository, and invokes Git's ordinary merge
semantics there. Source refs, objects, worktree and index are never mutated.
Symlinks and submodules are refused in this initial preview implementation.
Git hooks and user/system configuration are disabled. All temporary state is
removed before returning.

Execution uses the existing bounded test harness and sanitized environment,
with a timeout and process group termination on Unix. Windows uses the existing
direct-process termination policy; OS security isolation is not provided.
Supported harnesses are those used by `radar test`, including Go JSON tests,
Python unittest/pytest, Node tests, Jest/Vitest and Cargo summaries. A command
that exits zero with no recognized executed cases remains unknown. Missing
tools and unrecognized startup/build failures are execution errors.

With `--plan`, supported deterministic plan rules are evaluated against the
combined snapshot. Generic command observations do not satisfy plan-declared
acceptance evidence; those criteria remain unknown without matching evidence.

Evidence is returned in the report and binds the exact
combined commit and tree, ordered input commits, command, optional plan digest,
test counts and output digest. Raw test output is omitted to avoid exposing
secrets; unittest failures additionally expose bounded case identifiers and
source locations. Existing evidence from individual branches is never used as
proof of the candidate. A test command that changes tracked source or the candidate HEAD, or adds
untracked inputs outside known generated/cache directories, cannot
establish the original candidate's passing result.

The report compares declared and supported discovered contracts and reports
the static import dependency neighborhood. These analyses retain their normal
coverage limits and discovery candidates remain proposed. A passing test does
not prove every architectural property. Unknown or incomplete coverage returns
zero unless `--require-complete` is specified; supported failure returns one,
execution or invocation error returns two.

This demonstration reads TypeScript checkout configuration from Python. It
does not run a browser, start HTTP services or prove runtime serialization.
Its value is testing the real combined source against a supplied invariant,
while retaining intent and static contract evidence in one report.

Default preview and verification write no state into the original repository.
To retain metadata explicitly, use `--evidence-output .radar/evidence/candidate.json`
with verification, or redirect the JSON report to a file of your choice. Existing
artifacts are never overwritten.
