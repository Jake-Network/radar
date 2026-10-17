# Orchestrator integration recipes

These are local integration recipes, not claims of upstream adoption. Conductor,
cmux, claude-squad, Crystal and vibe-kanban can use the same subprocess boundary
before their merge action:

```sh
radar gate --json > radar-gate.json
```

Inspect both the process exit code and `gate.verdict`; only `pass` permits a
merge recommendation. `fail`, `blocked`, missing JSON, and process errors require
review. Keep `status`, `coverage`, checks and verdict separate. Static execution
does not establish runtime safety. For an explicit selected pair use
`radar gate branch-a branch-b --base main --json`. Do not interpolate branch
names into shell source: use an argv array in the host integration.

For MCP, launch `radar mcp` and use `radar_gate`. This tool does not authorize
repository execution. The server can write local state, so it is not read-only.
Use the existing [schemas](../../schemas/) as format contracts; additive JSON
fields should be tolerated. A public schema stability promise or marketplace
registration requires a release decision and is not established by these recipes.

A proposed upstream PR should include the host's exact merge callback, an
installation precondition, blocked/error tests and a screenshot from that host.
No upstream submission or acceptance has been performed by this change.


The stable single-repository verdict/check contract is
[`gate-report.schema.json`](../../schemas/gate-report.schema.json) v1. Workspace
reports use [`workspace-report.schema.json`](../../schemas/workspace-report.schema.json).
Consume JSON `gate.verdict`; additional evidence fields are additive and should
not be discarded when they describe missing or stale verification.

[`merge-callback.mjs`](merge-callback.mjs) is an executable Node adapter for an
orchestrator's pre-merge callback. It sends refs as argv, bounds time/output and
uses [`@radar/report`](../report/) to fail closed on unavailable JSON. The result
is a merge recommendation; the adapter does not mutate branches or execute tests.
The host can render `createSummary` and link the raw report beside its merge
button. A host integration must retain its own user confirmation and ref freshness
checks before mutation.

## Upstream integration drafts

The following are proposed insertion points inspected at the pinned upstream
revisions below. They are not installed integrations, submitted PRs, maintainer
agreements or host smoke-test results. Re-check the callback before preparing a
PR. Install Radar on the machine/runtime that owns the Git objects; enable the
integration explicitly in host settings.

Resolve the destination and selected source refs to immutable SHAs first. Invoke
an argv array equivalent to `['gate', sourceSHA, '--base', targetSHA, '--json']`
with `cwd` set to that repository, using `inspectBeforeMerge` above. Include every
source SHA for an N-way decision. Only exit 0 plus `gate.verdict === 'pass'` can
recommend mutation; `landing.restack_first`, missing inputs, blocked and errors
must remain visible. Re-resolve refs immediately before the host mutation and
rerun on movement. A static recommendation does not execute tests. The host
retains its existing user confirmation and authorization.

### Vibe Kanban: guard direct workspace merge

At [`d5cbb5380fa0b32e98ef9b8d987f63decce4be3a`](https://github.com/BloopAI/vibe-kanban/blob/d5cbb5380fa0b32e98ef9b8d987f63decce4be3a/packages/web-core/src/shared/hooks/useMerge.ts),
`useMerge().mutationFn` calls `workspacesApi.merge(workspaceId, {repo_id})`.
The server's [`merge_workspace`](https://github.com/BloopAI/vibe-kanban/blob/d5cbb5380fa0b32e98ef9b8d987f63decce4be3a/crates/server/src/routes/workspaces/git.rs#L179)
resolves the repository/workspace and invokes `deployment.git().merge_changes`.
Draft the server guard immediately before that mutation, after existing remote
branch/open-PR checks. Use `repo.path` as `cwd`, resolve
`workspace_repo.target_branch` as target and `workspace.branch` as source, then
carry both SHAs through the mutation freshness check. Render diagnostics through
the existing mutation error/result UI; do not guard only the button.

Host acceptance cases: a pass reaches `merge_changes` once; blocked, missing
Radar, a moved branch and unreadable JSON reach it zero times. Confirm an open
PR still rejects direct merge and a different `repo_id` cannot reuse the report.
No host test or screenshot has been produced.

### Mux / Xum: guard patch application only with source provenance

The Mux repository redirects to Xum. At
[`518f87f564f5a6de0a52dc0f78a2467552573b85`](https://github.com/coder/xum/blob/518f87f564f5a6de0a52dc0f78a2467552573b85/src/node/services/tools/task_apply_git_patch.ts),
`createTaskApplyGitPatchTool().execute` calls
`taskGitPatchEngine.applyPatch` under `withGitPatchArtifactOperationLock`.
The engine's [`applyProjectPatch`](https://github.com/coder/xum/blob/518f87f564f5a6de0a52dc0f78a2467552573b85/src/node/services/taskGitPatchEngine.ts#L1231)
validates `expectedHeadSha` before its real `git am` path. This is patch transfer,
so the generic merge adapter cannot establish equivalence by itself.

Draft a guard inside that operation lock before real apply: use `repoCwd` and the
current destination SHA, and require the artifact producer's source SHA and Git
objects in the same runtime. Run the argv above only after verifying that the
patch series represents that exact source delta. Patch-only artifacts, absent
objects, multi-project ambiguity or an unmapped runtime remain unavailable;
do not auto-fetch or treat the existing dry-run as semantic verification.

Host acceptance cases: blocked never calls real `git am`; expected-head movement
still rejects; dry-run remains non-mutating; no local binary is run against a
remote runtime by accident. Source-object/delta mapping and host execution tests
remain implementation prerequisites, rather than an integration claim.

### Emdash: gate the service before remote PR mutation

At [`79f37d920bd576dc18ca5421856fd2cc9ec8abac`](https://github.com/generalaction/emdash/blob/79f37d920bd576dc18ca5421856fd2cc9ec8abac/apps/emdash-desktop/src/core/services/pull-requests/node/wire-controller.ts#L52),
the wire controller's `mergePullRequest` runs the service operation. The
[`PullRequestService.mergePullRequest`](https://github.com/generalaction/emdash/blob/79f37d920bd576dc18ca5421856fd2cc9ec8abac/apps/emdash-desktop/src/core/services/pull-requests/node/pull-request-service.ts#L599)
checks repository registration then calls `engine.mergePullRequest`.
Draft the guard between registration and that engine call. Resolve the registered
repository to an authorized local checkout, obtain the PR's exact head/base SHAs,
and run the same argv there. A repository URL alone is insufficient: if either
object or the checkout mapping is missing, return unavailable without fetching.
Require the provider's head-SHA precondition and revalidate the base before merge.

Host acceptance cases: blocked/error cannot call the engine; a changed PR head or
base invalidates a pass; bypassing provider requirements does not bypass Radar;
a remote-only PR is visibly unavailable. Preserve abort signals and existing
post-mutation refresh. Local checkout mapping, provider preconditions, host tests
and UI screenshot still need implementation/verification.
