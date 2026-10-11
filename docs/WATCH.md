# Watch agents while they work

`radar watch` repeatedly combines selected branches in a private Git repository
and uses the same integration analysis, policy and test selection as `radar gate`.
It does not merge changes into your checkout. Run it from one repository while
agents edit separate linked worktrees:

```sh
radar watch --base main
radar watch --base main agent-api agent-web
radar watch --base main --include-dirty
radar watch --base main --include-dirty --run --suite full --jsonl
```

The default is static verification of commits. `PASS (static)` means required
static checks passed; it does not mean tests passed. Dirty changes are reported
as excluded unless `--include-dirty` is present. With no selected changes,
watch waits and reports `NOT VERIFIED` rather than a vacuous pass.

## Options

| Option | Purpose |
| --- | --- |
| `--base REF` | Integration base; defaults follow `gate`. |
| `BRANCH ...` | Pin the monitored branch selection; omit to discover worktree branches beyond the base. |
| `--include-dirty` | Preview staged, unstaged and nonignored untracked changes in selected worktrees. |
| `--interval 1s` | Portable polling interval (100ms to 1m). |
| `--debounce 500ms` | Wait for changes to settle before checking (0 to 1m). |
| `--jsonl` | Emit one JSON event per line for consumers. |
| `--run` | Authorize repeated selected test execution for this session. |
| `--suite full` | Use full test selection rather than the default balanced suite, with `--run`. |
| `--timeout 10m` | Fast Gate execution budget; optional attribution has a separate budget of this duration. |
| `--max-commands 16` | Bound selected commands per check; omitted required commands block the gate. |
| `--policy PATH` | Require declared checks; policies do not authorize execution. |
| `--attribution-runs N` | With `--run`, bound optional diagnostic subset executions; watch defaults to zero. |

`--run` authorizes repository code repeatedly, including new worktrees discovered
during an automatically selected session. The command prints this scope on
stderr. Code runs with your user permissions. A private Git checkout is not an
OS sandbox. No dependencies are installed and no network fetch is added.

## Dirty previews

The opt-in snapshot copies relevant changed regular files into private Git state.
It records staged and unstaged provenance independently, including deletions,
renames and nonignored untracked paths. It leaves source refs, indexes and files
untouched. Ignored dependency directories are not copied automatically.

Before-and-after observation rejects a snapshot that changed while being read.
Unsafe paths, symlinks, submodules, unsupported entries and snapshots that cannot
be represented faithfully produce `NOT VERIFIED` or `ERROR`, with a reason.
They never silently fall back to committed inputs and claim the current working
state passed. Dirty previews have distinct candidate identities and provenance;
their evidence does not prove the committed branches passed.

An unsafe or inconsistent initial snapshot rejects the invocation explicitly.
If it appears during the session, an error event invalidates the previous result
until observation succeeds again. Snapshot capture is capped at 4,096 changed
paths and 32MiB of changed contents per selected worktree, with the existing
per-file size limit. Sparse and assume-unchanged index entries are unsupported.
Executable mode changes are included even when `core.fileMode=false` hides them
from Git status. Dirty previews reject active `core.autocrlf` and Git `text`,
`eol`, `working-tree-encoding` or `ident` normalization, including clean-status
worktrees, until those transformations can be faithfully reproduced. Explicit
`-text` with raw file bytes is supported.

Only include worktrees you intend to inspect. Nonignored untracked files may
contain sensitive data; use `.gitignore` for local secrets. Unsaved editor buffers
are outside the filesystem snapshot and are not checked.

## Events and scheduling

The terminal lists worktrees with uncommitted changes and revision identifiers,
invalidates the previous verdict when inputs change, and reports `CHECKING`
and then the same verdicts and report as `radar gate` (`PASS (static)`,
`FAIL`, `NOT VERIFIED`, `ERROR`, ...), headed `Radar watch:`. The next step
says what to change; watch reruns by itself. Color is decoration; the words
remain readable in plain output. Unchanged inputs do not trigger another
check, and a one-line heartbeat (`still watching: ...`) is printed every 30
seconds.

JSONL events have an increasing sequence number, UTC timestamp, event type and
input state. Types are `changed`, `checking`, `completed`, `error` and
`heartbeat`, plus `attributed` when optional diagnostic runs finish. A `completed`
event contains the Fast Gate report; a heartbeat does not repeat
an old report. Consumers must invalidate their current verdict on `changed` or
`error`, and correlate results with their input key. Source changes detected
during analysis cancel or supersede the old check. Watch reobserves inputs before
publishing a result so a detected obsolete pass is never presented as current.

The JSONL envelope is described in [`schemas/watch-event.schema.json`](../schemas/watch-event.schema.json). It also reports `verification_state` (`NOT VERIFIED`, `CHECKING`, `ERROR` or the gate's verdict text, such as `PASS (static)` or `FAIL`), `last_completed` and `last_change`.

The scheduler has one analysis worker and coalesces saves during the debounce
window. Its key binds the base commit, selected branch commits, worktree state,
dirty digests when enabled, and relevant configuration. The existing integration
engine owns candidate construction and verification; watch adds scheduling. A single cached report reuses pinned analysis and evidence when only excluded dirty monitoring metadata changes; attribution-enabled sessions retain their separate candidate execution path.

Sessions are bounded to 32 monitored worktrees and 32 selected branches, and
dirty sessions to eight selected worktree snapshots. Exceeding a bound is an
explicit error. Dirty observation reads and hashes selected changed contents
twice to detect races, so its content-reading cost grows with changed bytes;
Git status and index inspection also depend on repository size. Capturing a
stable dirty digest still incurs polling I/O even when analysis is skipped.

Fast Gate evaluates the full combined candidate first. Observed Attribution is
a separate, optional diagnostic path that reruns failed commands on smaller
branch combinations. These runs do not change the Fast Gate verdict or replace
its evidence. Watch emits `completed` before starting these runs and emits
`attributed` separately when they finish. Both paths share one worker slot and
cancellation scope but have separate timeout budgets. Attribution is a repair
lead, not proof of a unique cause, and
flaky tests can mislead it. Keep its budget at zero for the shortest watch loop.

## Demonstration and measurement

```sh
go build -o /tmp/radar ./cmd/radar
bash examples/watch/demo.sh /tmp/radar
```

The demo starts watch before agents commit. Each agent branch passes its Python
test alone; their combined candidate fails without a textual merge conflict.
A dirty repair then produces a distinct passing preview. It checks unchanged
source refs and index bytes, contiguous JSONL sequences and temporary-directory
cleanup after SIGINT. It prints measured check latencies for the machine running
the demo; these include polling, debounce and test execution and are not a
cross-machine performance claim.

Development measurement on 2026-10-11 (Linux WSL2, Python 3.12.3, Git 2.43.0,
Go 1.26.8; this three-source-file fixture, 100ms polling and debounce):

| Event | Time from edit initiation to observed completed result |
| --- | --- |
| First committed passing check | 1.396s |
| Two-branch combined failure | 1.839s |
| Uncommitted repair preview | 1.955s |

These are single observations and include fixture Git commands and, for the
failure, independent branch test checks. They do not establish throughput or
large-repository latency. The demo also observed no redundant check during six
unchanged polling intervals.

A separate one-file Python fixture with one already-committed worktree change
took 0.379s from process start through its first static passing report at the
same polling and debounce settings. It produced no execution evidence. This
smaller static startup measurement is not directly comparable to the executed
two-worktree demo above.

## Limits

- Watch monitors one Git repository and its linked worktrees. Cross-repository
  runtime verification is not added by this command.
- Polling can miss a change reverted completely between observations.
  Before-and-after snapshot checks also cannot prove no transient edit occurred.
- Detached worktrees need explicit committed SHA selection under existing gate
  semantics; dirty previews require an unambiguous supported worktree input.
- Static pass proves only required supported static checks. Unsupported test
  configurations, selection gaps, unavailable runners and snapshot failures
  remain visible and cannot satisfy required execution evidence.
- No OS sandbox, editor-buffer capture, automatic dependency installation or
  automatic fetch is provided. Large repositories still pay for change polling
  and for the existing candidate analysis when relevant inputs change.
- Ctrl+C cancels the active check and waits for temporary-resource cleanup.
  The session exit code reports shutdown or invocation errors; individual live
  gate verdicts belong to completed events, not the eventual session exit code.
