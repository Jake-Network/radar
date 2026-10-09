# Organization fixture

This small source repository demonstrates Python FastAPI/Pydantic declarations,
TypeScript and JavaScript consumers, a Go worker, and a Rust export formatter.
It is an analysis fixture, not a deployable application: the queue and dataset
store are in memory, principal injection and worker execution are incomplete.

`contracts/openapi.json` is an explicit checked-in contract.
`.radar/contracts.json` declares that `frontend/summary.ts` depends on its
`ExportSummary.dataset_id` and `ExportSummary.total` fields. Radar verifies the
declaration and schema change; it does not infer an arbitrary network call or
prove that the running backend emits this schema.

See [the demo](../../docs/DEMO.md) for executable indexing, preflight, and
cross-branch analysis commands. Dependencies are not needed for indexing;
running the Python service would require FastAPI and Pydantic.
