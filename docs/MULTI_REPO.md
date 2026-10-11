# Several repositories

A workspace groups local repositories for one `radar gate`. Each repository's
branches combine privately; declared producer/consumer links are checked across
repositories. Radar does not clone, fetch, deploy or call services.

## Quick start

```sh
radar workspace add ../payments
radar workspace show
radar gate                                  # automatic branches in every repo
radar gate orders:agent/api payments:agent/client
radar gate --again --run                     # latest commits, explicitly run tests
```

The workspace is found from any member repository or linked worktree.
`--run` executes repository code; authorize it separately from static checks.

## Naming and selection

| Input | Meaning |
| --- | --- |
| `orders:agent/api` | ref in a named repository |
| `payments:src/order.ts` | repository-relative path |
| `orders:openapi.json#/components/schemas/Order` | schema JSON pointer |
| `agent/api` | ref in the current repository |

Repo IDs start with a letter and contain letters, digits, `-` and `_`.
Filesystem arguments (`--with`, `workspace add`) remain ordinary paths.

Scope precedence is replay → committed team file → local registry →
single-repository mode. Repeat `--with PATH` to add repositories for one run;
`--only REPO` narrows scope and labels the report as a partial workspace.

Without positional refs, each repository selects worktree branches with
commits beyond its base, sorted by name. With any positional ref, only named
refs are combined; other repositories participate at their base. Argument
order is merge order within each repository. Detached worktrees need an
explicit `repo:SHA`.

Base precedence is `--base repo:REF` → team-file base → `origin/HEAD`'s branch,
main/master/trunk. Missing bases are errors with an example remedy.
`--policy` applies to every repository; `--plan` requires `--only` one repo.

`--again` repeats the previous selection method on latest commits, reporting
branches that joined/left an automatic selection. It rejects selection-changing
options. `--replay RUN` (or `last`) pins recorded commits and verifies candidate
trees, rather than using latest branches.

## Registry and team file

| | Local registry | Committed team file |
| --- | --- | --- |
| Location | user config `radar/workspaces.json` (`RADAR_CONFIG_DIR`) | home repo `.radar/workspace.json` |
| Contents | workspace/home names, repo IDs → absolute local paths | IDs/identities, bases, links, retirements |

A repository belongs to at most one workspace. `gate` reads the registry;
`workspace add`, `workspace remove` and the first `connect` update it.
The first `connect` repository becomes home.

The team file is read from home's base commit, never its working tree.
Uncommitted edits warn. With a team file, registered-but-omitted repos are
excluded with a note; unregistered declared repos error with an add command.
Identity is the root-commit set (`git:<sha>+...`), not remote URL or path;
mismatches fail.

## Cross-repository links

`.radar/contracts.json` binds contracts within one repository. Declare a
workspace link and consumer expectations with:

```sh
radar workspace connect orders:openapi.json#/components/schemas/Order payments \
  --fields total,status --direction response --source src/order.ts
```

Commit home's `.radar/workspace.json` and consumer `.radar/consumes.json`.
Use `--direction request` for request contracts; `--into repo:PATH` chooses
the worktree to write. The link identifies producer, consumer and direction;
the consumer file versions its consumed fields and optional source.

| Producer / consumer | Interpretation |
| --- | --- |
| base / base | existing declaration |
| candidate / base | producer ships first |
| base / candidate | consumer ships first |
| candidate / candidate | combined changes; decides verdict |

Intermediate failures are notes, not the deciding result. Unsupported links
or removed/narrowed obligations remain NOT VERIFIED. Retiring a link requires
a reasoned `retired` declaration. See [obligations](VERIFICATION.md#contract-obligations).

`cross_repo.status: passed` proves only declared, supported static links.
It does not prove HTTP behavior, deployment order or runtime interoperability.
Without a team file, the report states per-repo checks only / zero cross-repo
links checked. Discovered links are proposals: up to three appear in text with
`connect` suggestions; JSON includes all. They never affect the verdict.

## Reports, records and limits

Repository rows identify automatic, named or base-only inputs. Selected dirty
worktrees are listed, but committed gate inputs exclude them; JSON also includes
other dirty worktrees. Repair attribution is a lead, not proof of blame.
Exit codes are pass 0, fail/blocked 1, error 2. Missing repository paths error,
while available repositories still report. A failed run-record write is shown
as `run not recorded` and does not change the computed verdict or exit code.

Records live under user cache `radar/runs/<workspace>/` (`RADAR_STATE_DIR`),
outside repositories. One-off scopes use `_with-<repo id>-<hash>`; retain the
latest 50 runs per workspace. `--timeout` applies separately to each repo.

Symlinks/submodules cannot be combined; register submodule repos separately.
Cross-repository runtime scenarios, database/rollout safety and a pinned
`--workspace FILE` CI interface are not implemented.
The reproducible static-link demo is `bash scripts/demo_workspace_links.sh /tmp/radar`.
