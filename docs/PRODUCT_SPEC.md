# Radar product specification

Plan against the real codebase. Coordinate changes across agents. Verify what actually shipped.

Radar is a local-first, agent-agnostic Go CLI connecting source evidence, explicit contracts, structured plans and Git checkpoints. No LLM credentials, telemetry, cloud uploads or repository scripts are required. Initial supported languages: TypeScript/JavaScript, Python, Go and Rust, with syntax-level definitions/imports and source locations. Syntax does not prove runtime behavior or resolve arbitrary calls.

The first vertical slices index a multilingual repository into a versioned SQLite graph; prepare grounded context; validate proof-carrying task DAGs and explicit projected graph deltas; detect supported JSON OpenAPI/JSON Schema property incompatibilities using explicit consumer mappings; compare declared verification rules with repository artifacts. Unknown requirements remain unknown. Proposed architecture never becomes verified source evidence by approval alone.

CLI: init, doctor, index, graph, plan, preflight, tasks, approve, impact, scan, explain, test, verify. Machine output is JSON. Review declarations, committed verification and command-bound test evidence are explicit artifacts; consequential plan approval is an explicit artifact, not an automated Git mutation. No automatic merge/rebase/revert/push.

Acceptance scenarios A–F in the mission are the release gate. A milestone is complete only when its executable fixture passes. Partial compatibility coverage and unavailable semantic engines must be disclosed. Large files and generated trees are bounded/excluded; source, paths, schema references and Git refs are untrusted.
