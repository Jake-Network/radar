# Intelligent verification demo

```sh
GOCACHE=/tmp/radar-go-cache go build -o /tmp/radar ./cmd/radar
bash examples/intelligent-verification/demo.sh /tmp/radar
# Optional: preserve JSON reports and /usr/bin/time samples.
DEMO_ARTIFACT_DIR=/tmp/radar-demo-results bash examples/intelligent-verification/demo.sh /tmp/radar
```

Requires Bash, Git, Python 3, Node with `fetch`, GNU `/usr/bin/time`, and permission
to bind a loopback HTTP socket. No package installation or external network
access is needed. Sandboxed environments that prohibit socket binding cannot
run this demonstration's HTTP integration test.

The generated checkout has Python backend modules, FastAPI/Pydantic adapter
source, imported TypeScript response types, an executable JavaScript client,
backend and frontend unit tests, and a separate integration suite. The backend
agent changes catalog price from one to two units; the frontend agent changes
quantity from one to two. Each branch passes its recommended verification.
Git combines them without textual conflicts, but their combined HTTP response
and actual Node client calculation violate the approved two-unit checkout
budget. Radar recommends the integration suite from static repository evidence
and detects its failure. Reconciled quantity restores a passing selected gate.

`--suggest-tests` does not execute code. Execution requires both `--verify`
and `--allow-execution`; `--suite recommended` executes the proposed commands
against the private combined candidate. `gate.json` requires textual merge,
absence of supported authoritative contract incompatibilities, and recognized
passing combined execution. A passing gate does not erase unrelated analysis
coverage gaps or establish complete architecture correctness.

The script also measures three runs of manual private Git merge plus all three
test commands, and three runs of Radar analysis plus its recommended commands.
The tiny fixture, warm local caches and Linux temporary filesystem make these
comparative overhead measurements, not scalability or adoption evidence.
Inspect the emitted selection reasons and JSON observations to see which tests
were actually executed. Temporary branches and repositories are removed;
the checked-out Radar repository remains untouched.

Runtime scope is explicit: the offline server uses Python's standard-library
HTTP implementation, and the client performs a real loopback HTTP request using
Node `fetch`. FastAPI/Pydantic adapters are source evidence only, because these
packages are not prerequisites. The TypeScript counterpart is indexed but is
not compiled or executed. This generated fixture does not substitute for
validation on an independently maintained application or a production service.

## Local measurement

On 2026-10-09, the generated demo passed on Linux amd64 with Node 24.14.0.
Three warm runs on the Linux temporary filesystem produced:

| Operation | Wall seconds | Maximum RSS, KiB |
| --- | --- | --- |
| Manual private Git merges and all three test commands | 0.80, 0.78, 0.78 | 61,032; 60,148; 60,416 |
| Radar private merge, analysis, and three recommended test commands | 0.99, 0.98, 1.02 | 60,724; 60,856; 60,284 |

The manual test suite also catches the failure once someone selects and runs it.
Radar's demonstrated contribution is deterministic selection with evidence,
private combined verification, selected-policy gating, and structured reports.
These process-tree RSS samples include the test tools. Manual fixture copying
and checkout generation are excluded from both measurements. A prior negative
elapsed sample caused by a host clock adjustment was discarded; the script
labels such measurements unavailable. No speed advantage is claimed.
