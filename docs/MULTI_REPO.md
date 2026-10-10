# Several repositories

A workspace groups local repositories so that one `radar gate` checks all of
them. Typical case: one agent changes the backend, another changes the
frontend, and you want to know whether the two changes still fit.

Each repository is still combined and checked on its own, in its own private
Git state. On top of that, Radar checks the cross-repository links you declare.
It does not clone, fetch, deploy or call anything over the network.

## Quick start

```sh
radar workspace add ../payments     # this repository + payments
radar workspace show                # what radar gate would check
radar gate                          # every worktree branch in every repository
radar gate orders:agent/api payments:agent/client
radar gate --again --run            # same selection, latest commits, with tests
```

`radar gate` finds the same workspace from any member repository or any of its
worktrees.

## Naming

| Form | Meaning | Example |
| --- | --- | --- |
| `repo:ref` | a branch, tag or full SHA in that repository | `orders:agent/api` |
| `repo:path` | a path from that repository's root | `payments:src/order.ts` |
| `repo:path#pointer` | a JSON pointer inside a file | `orders:openapi.json#/components/schemas/Order` |
| `ref` | a ref in the current repository | `agent/api` |

Repo IDs start with a letter and contain letters, digits, `-` and `_`. Git
refs cannot contain `:`, so `repo:ref` is unambiguous. Arguments that take a
filesystem path (`--with`, `workspace add`) do not use the `repo:` prefix.

Outside a workspace, and without `--with`, arguments are parsed exactly as in
single-repository mode.

## Scope, branches and bases

Scope comes from the first match of:

1. `--replay RUN`: the repositories and commits recorded for that run
2. the team file, if the workspace has one (see below)
3. the local registry entry for the current repository
4. otherwise, single-repository mode

`--with PATH` adds a repository for one run (repeatable). `--only REPO` limits
the run to some repositories, and the headline then says
`partial workspace (k/N repos)`.

Without positional arguments, each repository contributes its worktree
branches that have commits beyond its base, sorted by name. With any
positional argument, only the named refs are used and every other repository
takes part at its base. Order of arguments is merge order within a repository.
Detached worktrees are never picked automatically; name them as `repo:SHA`.

Each repository's base is, in order: `--base repo:REF`, the `base` in the team
file, then the usual default (`origin/HEAD`'s branch, `main`, `master`,
`trunk`). If no base can be found, that repository gets an error with an
example `--base` value.

`--again` repeats the previous run's selection method on the latest commits. If
the last run was automatic, it selects automatically again and shows which
branches joined or left. It cannot be combined with options that change the
selection. `--replay RUN` (a run ID or `last`) rebuilds a recorded run's exact
commits and checks that the candidate trees still match.

`--policy` applies to every repository. `--plan` is only allowed when `--only`
narrows the run to one repository.

## Registry and team file

| | Registry | Team file |
| --- | --- | --- |
| Location | `<user config dir>/radar/workspaces.json` (`RADAR_CONFIG_DIR`) | `.radar/workspace.json` in the home repository |
| Committed | no, it holds absolute paths | yes |
| Holds | workspace name, home repository, repo ID → path | repo IDs and identities, bases, links, retirements |

A repository belongs to one workspace at most. `gate` only reads the registry;
`workspace add`, `workspace remove` and the first `connect` write it.

The home repository is the one where you first ran `connect`. The team file is
read from the home repository's base commit, never from its working tree;
uncommitted edits to it produce a warning. When a team file exists, it decides
the scope. A repository that is registered but missing from the team file is
left out with a note. A repository listed there but not registered on this
machine fails with the `radar workspace add` command to fix it.

Repositories are matched by root commits (`git:<sha>+...`), not by remote URL
or path. A mismatch is an error.

## Cross-repository links

`.radar/contracts.json` describes contracts inside one repository. Workspace
links describe contracts between repositories.

```sh
radar workspace connect orders:openapi.json#/components/schemas/Order payments \
  --fields total,status --direction response --source src/order.ts
```

This writes the link into the team file:

```json
{"id": "order-response", "direction": "response",
 "producer": "orders:openapi.json#/components/schemas/Order", "consumer": "payments"}
```

and the consumer's expectations into `payments/.radar/consumes.json`, so they
are versioned with the consumer's code:

```json
{"version": 1, "consumes": [{"contract": "order-response", "fields": ["total", "status"], "source": "src/order.ts"}]}
```

Commit both files, as the command tells you. `--into repo:PATH` picks which
worktree to write into.

The gate checks each link in four combinations:

| Producer | Consumer | Question |
| --- | --- | --- |
| base | base | is the existing declaration valid? |
| candidate | base | producer ships first |
| base | candidate | consumer ships first |
| candidate | candidate | everything ships |

The candidate + candidate cell decides the verdict. Failures in the middle
cells are shown as notes. A link Radar cannot analyze, or one whose obligation
was removed or narrowed, leaves the gate `NOT VERIFIED`. Removing a link
needs a `retired` entry in the team file, as with single-repository contracts.

`cross_repo.status: passed` means the declared, supported static links passed.
It says nothing about rollout order or runtime HTTP behavior.

Without a team file, the headline says
`per-repo checks only · 0 cross-repo links checked`. If discovery finds
candidate links between changed repositories (same literal path and HTTP
method), up to three are shown with the `connect` command to accept them. The
JSON report has all of them. Suggestions never affect the verdict.

## Output and exit codes

From `bash scripts/demo_workspace_links.sh /tmp/radar`, after the producer
branch drops `total` (long lines shortened):

```text
Radar gate: FAIL — workspace "shop" · 2 repos · 1 branch · 1/1 cross-repo links checked

  orders    main@c33d6cb41643 + agent/api@db646baef75b   (named)
  payments  main@25f1381c7788                            (base only)

  ✓ orders    1 branch combined · no breaking contract change
  ✓ payments  base only · no breaking contract change
  ✗ link order-response  orders → payments: declared consumer field total is missing; … (candidate+candidate)
  ! link order-response  orders → payments: producer first fails — … (candidate producer + base consumer; excluded from verdict)

Repair leads (where to look, not proof of cause)
  orders:agent/api  openapi.json  declared consumer field total is missing; …
  payments:main  .radar/consumes.json  declares consumed fields

Next: repair and commit on the branches above, then: radar gate --again
```

Each repository line shows where its branches came from: `auto: worktree`,
`named` or `base only`. Uncommitted changes in a selected branch's worktree are
listed; the rest are in JSON only.

Exit codes are the same as for a single repository. A repository whose path is
missing makes the run exit 2, but the other repositories are still reported.
If saving the run record fails, the verdict and exit code do not change; the
report says `run not recorded`.

## Run records

Records live in `<user cache dir>/radar/runs/<workspace>/` (`RADAR_STATE_DIR`),
never inside a repository. A one-off `--with` scope uses
`_with-<repo id>-<hash>`. The newest 50 runs per workspace are kept.
`--timeout` applies to each repository separately.

## Limits

- A repository with symlinks or submodules cannot be combined. Register
  submodule repositories separately.
- Links compare schemas statically. Runtime compatibility, deployment order
  and database migrations are out of scope.
- Not built yet: cross-repository test scenarios that run against all
  candidates at once, and a `--workspace FILE` mode for CI that pins every
  repository by SHA.
