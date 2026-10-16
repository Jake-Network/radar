# Usage

Start with [`radar gate` in the README](../README.md). This reference covers
explicit inputs, continuous checking, contracts, plans and reproducible demos.
Run `radar help COMMAND` for flags or `radar help --all` for every command.

## Output and diagnosis

Add `--json` for machine output. Human reports retain the same symbols and words
with or without color; JSON and MCP output are never styled.
`--color auto|always|never`, `NO_COLOR`, `FORCE_COLOR` and `CLICOLOR_FORCE`
control color.
Terminal gate progress goes to stderr and is erased before the report.

`radar doctor` reads Git metadata, committed files and bounded local resource
configuration to report the base,
participating worktree branches, excluded dirty changes, discovered languages
and test frameworks, available runner programs, and a next command. Its JSON
adds these facts under `project`. Runner availability does not establish that
framework dependencies are installed: `node_modules` is not copied into private
candidates, and frameworks such as pytest are checked when tests run.

Doctor also warns when worktrees declare the same literal database URL, port or
Compose project name in conventional root `.env`/Compose files. Values are
compared privately and omitted from reports. Each warning lists only worktrees
sharing that same literal value; different values under one key stay separate.
Expressions remain `unknown` and are not assumed to share a literal resource.
Static `.pth` inspection of `VIRTUAL_ENV` warns about external paths or
uninterpreted loaders without running Python. These are configuration advisories,
not runtime isolation checks; nested settings and full Compose semantics are
not interpreted.

Candidate source integrity compares tracked blobs and files with bounded
streaming buffers, so total tracked bytes have no integrity limit. The 10,000
tracked-file and 20,000 filesystem-input bounds still apply. The separate
single-checkpoint `radar test` command copies at most 10,000 tracked entries
and 64 MiB of regular-file content into its execution snapshot; a larger input
is rejected before setup or tests run. That copy limit does not apply to
integrity inspection of private Git candidates used by `gate --run` or
`merge-check --verify --allow-execution`. Static parsing retains its own limits.
Same-length content changes, extra
inputs and supported executable-mode changes invalidate execution proof.
Declared runner preparation errors appear in `runner_preparations[].error` and
human output; inspection errors remain distinct from source mismatches.

Static Suggested tests show the actual targeted selection first. Conservative
package-root fallbacks are summarized with their count and `--suite balanced`;
JSON `verification_proposal.commands` retains the full proposal.

## Explicit inputs

`gate` selects the base and worktree branches, recommends tests and supplies a
next action. It checks committed inputs; staged, unstaged and untracked changes
are excluded. Use `merge-check` for exact branch order or execution commands:

```sh
radar merge-check --base main --branches feature/api,feature/web --suggest-tests
radar merge-check --base main --branches feature/api,feature/web \
  --verify --allow-execution --suite balanced
radar merge-check --base main --branches feature/api,feature/web \
  --verify --allow-execution --cwd services/api -- python3 -m unittest test_api
radar check --base main --head feature/api --suite targeted
```

Branch order can change the combined result. `check` analyzes one head (the
working tree by default), including reverse import impact and contract changes;
it does not combine branches. Its `--suite` previews selection without execution.
`--evidence-output .radar/evidence/candidate.json` saves merge-check execution
metadata to a new file; candidate construction leaves source refs and files alone.

`gate --run` authorizes selected test execution with a default balanced suite,
10-minute budget and 16-command limit. Gate and watch default to zero optional subset
diagnostic reruns. Failed commands receive one automatic base comparison
within the shared command/time budget; failures reproduced there leave
verification incomplete. `--run --attribution-runs 8` opts into smaller branch-subset executions.
These observations are repair leads, do not change the verdict, and are not
available in `merge-check`. Selection modes, omitted-command handling, policies,
contract obligations and exit codes are in [VERIFICATION](VERIFICATION.md).

## Landing parallel branches

```sh
radar land api client docs --base main
radar land api client --base main --conflict-brief --json
radar land api client --base main --apply
# Explicitly authorize an installed structural merge driver:
radar land api client --base main --driver mergiraf --allow-execution
```

Landing reports pair conflicts, ancestry constraints, a deterministic order and
independent branches. Up to 12 branches use all pairs and exact optimization of
a pair-based rebase estimate; larger sets use changed-file overlap and a greedy
order. Estimates are not measured rebase counts, and omitted pairs remain
unverified. A conflict brief requires exactly two branches and includes bounded
committed source/hunks, inferred symbols and related tests, and reproduction
arguments. Related tests are static leads, not observed coverage.

`--apply` publishes only `refs/radar/integration/<run-id>` after a static PASS;
it preserves branch refs, index and working files and prints a fast-forward
command when ancestry permits. Review the result and select the intended base
before running that command. Tests require a separate `gate --run`.
Pass the same `--policy PATH` and `--plan PATH` to landing as to gate; missing
requirements or configuration changes prevent ref publication.
Branches that conflict with base alone appear in `landing.restack_first`. Their
names and pinned commits remain in `landing.branches`, while pairs, order, and
candidate evidence cover the remaining inputs. The required `landing_input_scope`
check keeps the full selection NOT VERIFIED even under a custom policy; failures
in the remaining combination still fail. If every input needs restack, no
candidate commit or tree is reported. `land --apply` refuses these plans before
importing objects or creating a ref.

`landing_apply` reports whether a ref was published, whether immutable candidate
objects were imported, and the refusal reason. Imported objects may remain after
a refusal; they do not update branches or establish verification. Existing base
and branch history is excluded from the candidate object pack.
`mergiraf` and `weave` require `--allow-execution`, run with host permissions in
the private candidate, and record successful repairs as `resolved_by`; merely
installing a driver does not authorize it. MCP `radar_land` and
`radar_conflict_brief` expose static previews only. Workspace landing adds
repository-local plans and declared cross-repository contract comparisons;
it does not perform cross-repository runtime tests or apply refs atomically.

## Recording a verified run

```sh
radar gate api client --base main --trailer
radar show RUN-ID --json
```

Only a passing gate emits a `Radar-Verified:` trailer and saves its immutable
report in local Radar state. The trailer lists supported static checks and
observed tests/selected commands, or `not_run`. Append it to a commit message
only after reviewing the result. `show` reads the historical candidate report;
it does not verify today's HEAD. Records bind repository identity and content
digests, not an authenticated signer, and require the original local state.
For PR check runs and report types, see
[Actions integration](../integrations/github-actions/README.md) and
[report package](../integrations/report/README.md).

## Agent setup

```sh
radar setup claude                 # or codex, both; no name defaults to codex
radar setup both --dry-run
radar setup claude --base main --branches feature/api,feature/web
```

Setup installs project-local hooks, skills and MCP configuration while preserving
unrelated settings and rejecting conflicting Radar entries. Agent trust, restart
and PATH requirements, hook behavior and installation options are documented in
[AGENT_GUIDE](AGENT_GUIDE.md). `radar help agents` prints the shorter agent contract.

`radar mcp` serves stdio tools, including `radar_gate`, `radar_check` and
`radar_merge_check`. Execution flags, `test` and `approve` are not exposed.
Summaries are capped at 40,000 bytes; request `detail: true` for the full report
or use CLI JSON. MCP writes Radar state and plans; it is not read-only.

## Watch

Watch runs the gate analysis repeatedly in private Git state while agents work
in linked worktrees. It never merges into your checkout.

```sh
radar watch --base main
radar watch --base main agent-api agent-web
radar watch --base main --include-dirty
radar gate --base main --overlap --file src/api.py --json
radar watch --base main --include-dirty --run --suite full --jsonl
```

Omit branches to discover worktree branches beyond the base; supply branches to
pin participation. Default checks are static and committed-only. `PASS (static)`
does not establish passing tests. With no selected changes, watch waits and
reports `NOT VERIFIED`.

| Option | Behavior |
| --- | --- |
| `--include-dirty` | Preview saved staged, unstaged and nonignored untracked changes. |
| `--interval 1s` | Poll interval, allowed range 100ms–1m. |
| `--debounce 500ms` | Wait for saves to settle, allowed range 0–1m. |
| `--jsonl` | Emit structured events, one per line. |
| `--run` | Authorize repeated repository-code execution for the session. |
| `--suite full` | Select the full supported inventory, within the command budget. |
| `--timeout 10m` | Execution budget; optional attribution has a separate budget. |
| `--max-commands 16` | Omitted required commands block the gate. |
| `--policy PATH` | Select required checks; does not authorize execution. |
| `--attribution-runs N` | Optional diagnostic subset runs with `--run`; default zero. |

Automatically selected `--run` sessions also authorize newly discovered
worktrees; watch prints this scope on stderr. Code runs with your permissions.
Private candidates are not an OS sandbox; Radar does not install dependencies
or fetch history.

Saved previews also report supported pairwise `drift` findings across worktrees.
`gate --overlap` provides an on-demand saved-state map; `--file` and `--symbol`
filter findings, and MCP exposes `radar_overlap`. Drift is an inferred repair
lead. No findings does not prove absence of cross-worktree relationships or
runtime compatibility. Watch completion JSON carries the current drift list.

### Saved snapshots

`--include-dirty` records staged and unstaged provenance, deletions, renames,
nonignored untracked files and executable-mode changes, including changes hidden
by `core.fileMode=false`. Source refs, indexes and files remain untouched.
Unsaved editor buffers and ignored files are excluded. Preview evidence has its
own candidate identity and does not qualify the committed branches.

Snapshot capture checks inputs before and after reading. Unsafe paths, symlinks,
submodules, unsupported entries or inconsistent reads produce an explicit error
or `NOT VERIFIED`; Radar does not fall back to commits and report the current
saved state passed. Initial snapshot errors reject the invocation; errors during
a session invalidate the previous result until observation succeeds again.

Limits are 4,096 changed paths and 32MiB of changed contents per selected
worktree, plus the per-file limit. Sparse and assume-unchanged entries are
unsupported. Dirty previews reject active `core.autocrlf` and Git `text`, `eol`,
`working-tree-encoding` or `ident` normalization, even in clean worktrees;
explicit `-text` with raw bytes is supported. Only select worktrees you intend
to inspect; use `.gitignore` for local secrets.

### Events and current results

Watch invalidates the previous verdict on input changes, reports `CHECKING`,
then publishes the gate result. Unchanged inputs do not trigger another check;
a heartbeat appears every 30 seconds. Saves are coalesced during debounce, with
one analysis worker. Inputs are observed again before publishing; changes during
analysis cancel or supersede the old check.

JSONL follows [`watch-event.schema.json`](../schemas/watch-event.schema.json):

- Events carry an increasing `sequence`, UTC `time`, `type` and `state.key`.
  Types are `changed`, `checking`, `completed`, `error`, `heartbeat` and optional
  `attributed`.
- **Invalidate the current verdict on `changed` or `error` and correlate results
  with `state.key`.** `completed` carries the gate report; a heartbeat does not
  repeat a report. The key binds base and branch commits, worktree state, dirty
  digests when enabled, and relevant configuration.
- `verification_state` describes the current state: `NOT VERIFIED`, `CHECKING`,
  `ERROR` or a verdict such as `PASS (static)` or `FAIL`.
- `last_completed` and `last_change` are event timestamps, initially
  `0001-01-01T00:00:00Z`. An old completion does not establish current validity.
- `state.value.worktrees[].commit_included` identifies participating committed
  inputs; `uncommitted_included` identifies selected saved snapshots.

Optional attribution follows `completed` and publishes `attributed` separately.
It shares the worker and cancellation scope, has a separate timeout budget,
and cannot replace the full-candidate evidence; flaky tests can mislead it.

Sessions support at most 32 monitored worktrees and 32 selected branches, with
at most eight dirty snapshots. Exceeding a bound is an error. Dirty observation
reads changed contents twice to detect races, so polling cost grows with changed
bytes and repository size even when analysis is skipped. Polling can miss edits
fully reverted between observations. Watch covers one repository, not runtime
integration across repositories. Detached worktrees require explicit committed
SHA selection; dirty inputs must map unambiguously to supported worktrees.
Ctrl+C cancels analysis and waits for cleanup. Session exit codes describe
shutdown or invocation errors; live verdicts belong to completion events.

## Explicit contracts

Commit `.radar/contracts.json` beside an OpenAPI JSON/YAML or JSON Schema:

```json
{
  "version": 1,
  "bindings": [{
    "id": "organization-summary",
    "schema": "openapi.yaml",
    "pointer": "/components/schemas/OrganizationSummary",
    "producer": "backend/models.py",
    "consumer": "frontend/summary.ts",
    "direction": "response",
    "fields": ["total", "owner.id"]
  }]
}
```

`radar contracts` lints declarations and text-searches consumer fields; it does
not prove runtime usage. `impact` compares revisions and `scan` checks concurrent
branches. `discover --json` proposes candidates without editing the manifest.
Removing a declaration does not remove its base obligation. See
[CAPABILITIES](CAPABILITIES.md#contracts) for supported comparisons and
[VERIFICATION](VERIFICATION.md#contract-obligations) for retirement rules.

## Plans and evidence

Initialize with `radar init` or `setup`. Linked worktrees without their own
`.radar` share main-worktree state; repository identity uses the root commit.

```sh
radar index --ref BASE_SHA
radar plan "add async exports" --ref BASE_SHA --output .radar/plans/design.json
radar preflight --plan .radar/plans/design.json --ref BASE_SHA
radar tasks --plan .radar/plans/design.json
# Only after an actual review:
radar approve --plan .radar/plans/design.json --reviewer NAME \
  --output .radar/plans/approved.json
radar test --plan .radar/plans/approved.json --ref IMPL_SHA \
  --allow-execution --timeout 30s -- python3 -m unittest test_export
radar verify --plan .radar/plans/approved.json --ref IMPL_SHA --evidence EVIDENCE_ID
```

`plan` creates context and an incomplete plan for your agent to fill in.
`preflight` rejects changed indexed baselines; `tasks` prints a DAG and packets,
keeping tasks sharing components or contracts out of parallel groups.
`approve` records the supplied reviewer declaration, bound to the plan digest;
editing the plan invalidates it. This is not identity authentication.

`test` runs a plan-declared `test_run` command on a private committed copy with
explicit execution authorization. Rules may declare `cwd`, environment variable
names to pass through, linked untracked directories, setup commands and a JUnit
file or directory of `TEST-*.xml` reports. Records store outcome and output
digest, not raw output. `verify` rejects evidence from another commit or plan.
Open assumptions keep verification `unknown`; accept or resolve them rather
than deleting them.

Use `resolve NAME` for entity IDs, `graph` for index queries (`--plan PATH
--format mermaid` for plan intent), `affected --base main --head REF` for reverse
import impact and `explain FINDING_ID` for stored findings. IDs such as
`function:svc/server.go#Server.Handle` are relative to the repository.

## Demos

Scripts use checked-in fixtures and temporary repositories. Read them before
running: execution steps run fixture code. Build needs Go 1.23+, CGO and a C
compiler; scripts need Bash, Git and Python 3.

```sh
go build -o /tmp/radar ./cmd/radar
bash scripts/smoke.sh /tmp/radar
make demo-all                      # builds bin/radar and runs examples/*/demo.sh
```

Run any script below with `/tmp/radar` as its argument.

| Script | Demonstrates |
| --- | --- |
| [`scripts/smoke.sh`](../scripts/smoke.sh) | Two individually passing branches whose combination fails. |
| [`examples/quickstart/demo.sh`](../examples/quickstart/demo.sh) | Doctor, failure reproduction, repair and unverified docs-only change. |
| [`examples/integration/demo.sh`](../examples/integration/demo.sh) | Combined failure, repair and diagnostic attribution leads. |
| [`examples/watch/demo.sh`](../examples/watch/demo.sh) | Live committed failure, distinct dirty repair, source integrity and SIGINT cleanup. |
| [`examples/intelligent-verification/demo.sh`](../examples/intelligent-verification/demo.sh) | Cross-language selection and HTTP integration; needs Node with fetch, GNU time and loopback sockets. |
| [`examples/selection-eval/demo.sh`](../examples/selection-eval/demo.sh) | Contract obligations, explicit retirement and grouped targeted suites. |
| [`examples/organization/demo.sh`](../examples/organization/demo.sh) | Indexing, plan preflight and producer/consumer conflict; retains its temporary repository. |
| [`examples/verification/demo.sh`](../examples/verification/demo.sh) | Commit-bound evidence and invalidated review after plan edits. |
| [`scripts/demo_workspace_links.sh`](../scripts/demo_workspace_links.sh) | Declared static cross-repository link, breaking change and repair. |

The watch demo prints local measured latencies including polling, debounce and
execution. These fixture observations do not establish large-repository or
cross-machine performance. Multi-repository commands are in
[MULTI_REPO](MULTI_REPO.md).

## Advanced configuration

`radar gate` compares committed branches in private Git state. It does not fetch,
install packages, import repository Python, evaluate build scripts or run tests
unless execution was explicitly authorized. Unsupported input is reported as
unknown; a static PASS covers only supported required checks.

### Workspace imports and symbols

The shared worktree/commit indexer reads literal uv/Poetry, npm/pnpm/tsconfig,
Cargo, go.work, Maven and Gradle workspace declarations. Member edges use
`workspace_member` provenance and remain inferred. Literal member globs are
bounded; computed lists, escaping paths and ambiguous package owners are unknown.
uv/Cargo exclusions are honored. These edges improve affected-file and test
selection; they do not establish runtime import behavior.

`no_symbol_conflicts` compares supported declarations and explicit named imports
against the base, including deleted defining files and literal re-export chains.
Simple, explicitly bound newly added calls can reveal argument-count changes.
Duplicate definitions require a supported common namespace. Python modules are
separate namespaces; valid interface merging, rebinding, overloads and Java test
source sets are not treated as duplicate production definitions.

Unsupported receivers, dynamic exports and ambiguous resolution are retained in
an optional `symbol_resolution: unknown` check and per-file diagnostics. They
are excluded from the supported conflict scope. Missing/partial source parsing
blocks a supported check where the necessary declarations cannot be established.
C++ cross-translation-unit function ownership and complex overloads remain outside
this static scope. No relationship is compiler-resolved.

### External SCIP input

```sh
radar index --ref HEAD --scip index.scip --scip-binding index.binding.json
```

Both flags are required. Generate the index yourself; Radar runs no producer.
The binding has exactly these fields (replace placeholders with actual values):

```json
{
  "version": 1,
  "revision": "exact indexed revision from Radar's snapshot",
  "index_sha256": "lowercase SHA-256 of the index.scip bytes",
  "sources": {"src/api.py": "lowercase SHA-256 of that source file's bytes"}
}
```

Supply every indexed document's repository-relative path and source digest.
Stale revisions, mismatched bytes, malformed or unsupported input remain
unverified and are rejected. Imported occurrences add `verified_tool` links to
the structural graph; this is a producer assertion, not proof of compiler
execution or producer identity. Existing inferred relationships remain inferred.
An imported index does not automatically replace gate's candidate analysis.

### Migration, dependency and uniqueness checks

Conventional migration locations are detected automatically. Explicit locations:

```json
{"version":1,"locations":[{"path":"db/changes","tool":"alembic"}]}
```

Save as `.radar/migrations.json`. Tools include Alembic, Django, Flyway,
golang-migrate/SQL, sqlx, diesel, Prisma and Liquibase XML. Base defects are warnings;
new problems are compared with each branch merged alone onto the current base.
Prisma chronology is a warning, not a duplicate timestamp failure. Liquibase
includes/computed identities and ambiguous cross-file identity remain unknown.
Migration contents are not executed and SQL operation compatibility is not inferred.

`lockfile_consistency` compares direct dependency names against supported uv,
npm/pnpm and Cargo locks, or exact required Go module/version sums. Workspace
members use their own importer/project declarations. Literal Cargo workspace
inheritance is supported. This is neither dependency solving nor arbitrary
version-range compatibility. Diverging literal major-version changes are warnings.

`unique_keys` checks supported literal application route scopes for FastAPI,
Express, Gin, Spring and axum. Dynamic composition remains unknown. Explicit keys:

```json
{"version":1,"rules":[{"name":"error-code","files":["src/**/*.py"],"pattern":"ERROR_CODE = \"(E[0-9]+)\""}]}
```

Save as `.radar/unique.json`. A regex's first capture group is the key; without a
capture, the whole match is the key. Alternatively `json_pointer` can select a
string, string array or object keys. Regex rules intentionally may match comments;
choose narrow globs and patterns. Built-in route extraction excludes source-like
text inside comments and string literals. Unrelated handler/config edits do not
activate existing unsupported route histories.

### Contracts and generated outputs

Declared contract schemas can be JSON/YAML, `.proto`, `.graphql`, `.gql` or
`.graphqls`. Protobuf supports bounded literal message/enum definitions, numbers,
cardinality/types and reserved names/ranges. GraphQL supports literal named types,
fields/arguments/enums and supported nullability changes. Unsupported imports,
services/options/complex Proto constructs or GraphQL interfaces/unions/directives
and recursive types remain unavailable. Invalid or unresolved types do not pass.

`.radar/generated.json` declares generation:

```json
{"version":1,"generators":[{"name":"client","sources":["api/*.json"],"outputs":["client/**/*.ts"],"command":["python3","tools/generate.py"],"cwd":"."}]}
```

Static checks warn about stale source/output relationships. With `--run`, each
command runs in a separate private candidate. Matching output bytes establish
`generated_drift`; changes outside declared outputs invalidate the observation.
A contract binding may also declare `"generate":["python3","tools/spec.py"]`.
Base and combined specs are generated separately, with checkpoint-bound
`generated_executions` metadata and digests. Neither command success nor a digest
means tests ran.

### Runner profiles and observations

`.radar/runners.json` example:

```json
{"version":1,"profiles":[{"root":".","prefix":["uv","run","--frozen"],"setup":[["uv","sync","--frozen"]],"lockfiles":["uv.lock"],"artifacts":[".venv"],"exclude":["-m","not integration"]}]}
```

A profile matches its `root` and descendant command directories; the most specific
matching profile wins. Setup runs at `root`, and lockfiles and artifacts are
relative to that directory. Selected test commands keep their own CWD, so a
relative `prefix` executable resolves from that CWD, not from the profile root.
For a package-local `.venv/bin/python`, declare the package directory as the
profile root and use it as the selected command CWD. A root `uv run` profile can
cover nested commands through uv's project lookup; it does not relocate relative
executable paths.

On Windows, candidate-relative native runners use `.exe` or `.com` files rather
than Unix executable bits. Known `.cmd`/`.bat` build wrappers still receive the
static distribution-cache check, but direct batch-script execution is unsupported.
A batch runner needs an explicitly declared interpreter command; Radar does not
construct a shell command from repository paths or arguments.

Only `--run` (or the equivalent explicit merge-check opt-in) runs declared setup.
Explicit profile setup may download dependencies from registries; it keeps the
private scrubbed environment but removes package-manager offline switches.
Regular verification and legacy plan setup retain their offline policies.
Setup, generators and selected tests share the command budget; skipped required
operations block the gate. Reviewed plan setup also consumes this budget. A
source-changing setup cannot publish reusable artifacts. Setup and cache hits
never count as passing tests.

Dependency cache entries bind repository, profile, platform, lockfile bytes and
the candidate source tree, including local code installed by setup. Supported artifacts
are `.venv`, `node_modules`, `target`. The default cache limit is 512 MiB, with LRU
eviction and a configurable maximum of 10 GiB. Unsafe symlinks, sensitive files
and over-budget artifacts are rejected. Virtualenv console/editable paths are
relocated to the new private copy. Real package-manager/network/platform behavior
still needs project-specific execution evidence.

`radar gate --run --observe-coverage` observes Python file reads, including data
files and child Python processes. Other frameworks declare a fresh report in the
profile, for example `"coverage":{"format":"go","report":"coverage.out"}`
with an explicitly declared argv argument such as `-coverprofile=coverage.out`.
Formats are coverage.py JSON (`python`), Go coverprofile (`go`), JaCoCo XML
(`jacoco`), llvm-cov export JSON (`llvm`), and Istanbul/c8 JSON (`c8`). Report paths
are relative to the selected command's CWD. A report already present before the
command cannot supply fresh evidence. Reports are bounded and raw contents are
not persisted.

SQLite records associate an observed command suite with its selected test files,
not individual cases. Selection uses only relationships whose test/file bytes
match the baseline and whose recorded baseline is an ancestor of the current
base. Stale and unrelated records remain excluded. Coverage is saved only after
all executions establish unchanged candidate source. A fresh relationship augments
static impact; it never proves complete behavioral coverage.
