# Contributing to Radar

Use Go 1.23 or newer and a C compiler (Tree-sitter uses CGO). Start with:

```sh
go test ./...
go vet ./...
go build ./cmd/radar
```

Keep changes small and add deterministic fixtures for new analysis. Include
negative tests: a supported detector must not report an unsupported inference
as a confirmed violation. Evidence categories and diagnostics are public
behavior. An unavailable analyzer is unknown, never a successful verification.

For a language adapter, document structural versus semantic capabilities,
source locations, unsupported syntax, bounds, and dependency licensing.
For contract checks, state exactly which schema semantics are checked and
test malformed input. Avoid fixture-specific detection logic.

Run `gofmt` on changed Go files. Describe the behavior, the evidence behind it,
and the tests in your pull request. Never add telemetry or source uploads by
default. Do not execute repository-provided programs during indexing.

Package boundaries are described in [architecture](docs/ARCHITECTURE.md). Keep
command parsing in `internal/cli`, test execution in `internal/evidence`, and
analysis rules in their owning modules. `cmd/radar` remains an entry point.
When changing evidence or plan semantics, add stale-commit and stale-plan negative
cases and update the JSON schema, integration skills (Claude Code and Codex),
MCP tool descriptions and CLI demos together. New commands go in the command
table in `internal/cli/commands.go` with their own flags and a human renderer.
Entity identities must stay independent of the checkout location.
For release packaging and dependency notices, see [releasing](docs/RELEASING.md).

## Add a detector

Own one tool or language at a time. Start with its existing package (for example
`internal/migrations`, `internal/discovery`, `internal/indexer` or
`internal/contracts`) rather than adding a second CLI analysis path. Detectors
consume committed files as data. They must not execute repository programs or
fetch. Preserve the owner package's existing `Result`/finding contract; there is
no universal detector plugin interface. Integration wires results into checks
and the built-in policy only when applicable.

Write a temporary-repository fixture with base, branch A, branch B, each branch
merged alone onto base, and the combined tree. Include a confirmed positive,
a benign negative, a base-existing issue, a single-branch issue and
malformed/unsupported input. Base-existing issues must not be blamed on a new
branch. Unsupported input is unknown, never PASS. Findings need repository
relative files, locations, supported evidence method and a useful repair hint.
Avoid fixture-name detection and aggregate counts that lose identity.

Run the owner package tests, `go test ./...`, `go vet ./...`, build and
`git diff --check`. Gate changes additionally need the smoke/demo checks; public
JSON/MCP or evidence changes need schema and agent-asset updates. Use
[the detector issue template](.github/ISSUE_TEMPLATE/detector.md) to propose a
bounded rule with positive and negative fixtures.

## First contribution drafts

These are unfiled issue drafts, not confirmed bugs or assignments. Before opening
one, reproduce the gap against current tests and narrow it to one fixture or
renderer. Each should include a benign counterpart and avoid new dependencies.

| Draft | Scope and acceptance fixture |
| --- | --- |
| 1. Explain optional test fallbacks | `internal/cli/suggested_tests_test.go`: all-fallback and mixed proposals keep the count visible; JSON is unchanged. |
| 2. Keep grouped suggestions readable | The same test: two related Go/pytest commands group exactly as targeted execution does. |
| 3. Bound long suggestion lists | The same renderer: six direct commands show five plus the correct remaining count. |
| 4. Keep resource advice deterministic | `doctor_resources_test.go`: permute collector order and require identical kind/key rows and sorted worktrees. |
| 5. Avoid duplicate editable advice | The same file: multiple `.pth` files collapse by kind/key while unknown loaders remain unknown. |
| 6. Explain oversized discovery input | The same file: an oversized source is informational; an oversized test/configuration blocks inventory. |
| 7. Keep doctor hints portable | `ux_test.go`: an unavailable worktree is reported consistently with native Windows paths. |
| 8. Preserve setup causes in workspace output | `suggested_tests_test.go`: setup inspection failures appear in text and remain bounded in JSON. |
| 9. Test color-independent advice | `style_test.go`: strip styling and compare the meaning of fallback/setup messages. |
| 10. Preserve user-edited skills | `internal/onboarding/setup_test.go`: legacy user edits survive repeat setup and migration attempts. |
| 11. Keep embedded skills synchronized | Onboarding asset tests: compare canonical skill bytes with shipped copies. |
| 12. Reject incomplete merge advice | Add `integrations/examples/merge-callback.test.mjs` for the existing adapter: empty/truncated JSON and null exit status cannot recommend merging. |
| 13. Preserve additive report fields | `integrations/report/test.mjs`: a new optional diagnostic does not erase blocked status. |
| 14. Explain shallow history | `internal/cli/gate_reliability_test.go`: the error recommends fetching history without performing a fetch. |
| 15. Test resource expression privacy | `doctor_resources_test.go`: shell expressions and URL credentials never appear as advisory values. |

Confirm test filenames before starting; nearby existing cases may already cover
the proposed fixture. Closing a draft with evidence of existing coverage is useful.

## Interview recruitment and script

Recruit ten people who have used at least two concurrent coding-agent worktrees
in the past month: four TypeScript/JavaScript, three Python and three Go users.
Across these groups aim for two Windows, two macOS and six Linux users, including
at least three maintainers reviewing other people's branches. These are recruiting
targets; no interviews or contacts are claimed here.

Use a 30-minute session: ask for the last integration failure (5 minutes), walk
through how it was detected and repaired (10), try `doctor` and a static `gate`
on a participant-approved example (10), then ask what would justify repeat use
and where they would put a pre-merge check (5). Ask about false alarms, blocked
results, acceptable latency, installation friction and whether a useful problem
could have been predicted before agents started. Do not prompt for a positive
verdict or request source uploads. Repository-code execution needs separate
explicit consent.

With permission, record anonymized workflow, OS/language, observed command,
verdict interpretation, time to a usable next action and repeat-use intent.
Separate observed behavior from opinions and missing answers. Ten completed
sessions, recurring pain with concrete examples, and documented counterexamples
are the evidence needed for a product decision; a recruitment list is not a result.
