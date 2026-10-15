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
