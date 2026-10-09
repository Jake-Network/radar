# Two agents, one broken checkout

Run `bash examples/integration/demo.sh` from the Radar repository. Requires Git,
Go (or `RADAR_BIN=/absolute/path/radar`) and Python 3. No package installation or
network access is needed.

A Python backend agent doubles a product's price. A TypeScript frontend agent
doubles the checkout quantity. Each branch passes the same checkout budget
test independently. Git merges both files without a conflict, but their
combined checkout exceeds the budget. `merge-check` runs the test against the
combined tree, records a failed unittest case and source location, and then
verifies the repaired quantity against a different combined tree.

The TypeScript client configuration is inspected by a Python integration test;
this small demonstration does not run a browser or establish real HTTP
serialization. Radar does not hardcode its invariant or the expected failure.
The script asserts the observations produced by the shared test harness.

The candidate has private Git objects, refs, index and working files. The
source repository is never merged into. Temporary state is removed on success
and failure. Execution remains an explicit authorization to run repository
code with host privileges, **not an OS sandbox**.

Reports remain incomplete for unestablished properties even when the supplied
test passes. Ordinary unknown coverage is informational (exit 0); supported
failures return 1, invocation/execution errors return 2. Use
`--require-complete` to opt into rejecting incomplete coverage.
