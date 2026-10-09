# Intelligent verification validation

These observations were collected locally on 2026-10-09 with Linux amd64,
Python 3 and Node 24.14.0. They describe particular fixtures and pinned source
changes, not adoption, complete coverage, recommendation precision, or
production readiness. The full regression results are reported separately.

## Reproducible multilingual runtime demonstration

```sh
GOCACHE=/tmp/radar-go-cache go build -buildvcs=false -o /tmp/radar ./cmd/radar
DEMO_ARTIFACT_DIR=/tmp/radar-intelligent-results \
  bash examples/intelligent-verification/demo.sh /tmp/radar
```

The generated fixture contains separate Python backend modules, static
FastAPI/Pydantic source, imported TypeScript response types, a Node fetch client,
Python and Node unit tests, and a separate Python integration test. No external
dependencies are downloaded. The runtime server uses Python's standard-library
HTTP implementation; FastAPI/Pydantic are not imported and TypeScript is not
compiled. The test starts a loopback HTTP server and invokes the actual Node
client, which calculates checkout total from the returned response.

Each agent branch first runs the complete three-command fixture suite in a
private copy and passes. Each also passes its Radar-recommended selected gate.
Their combined Git merge has no textual conflict. Price two multiplied by
quantity two violates the approved two-unit budget, so the combined HTTP/client
integration test fails. Reconciled quantity passes the recommended suite and
selected gate. The original fixture checkout remains on its baseline, with no
tracked changes, and all temporary repositories are removed.

For combined changes, recommendations include the integration test with
`cross_component_integration`, the frontend unit test with `dependency_impact`,
and the backend unit test with `package_fallback`. These are inferred selection
reasons, not proofs of complete test coverage. The read-only suggestion report
and each selected/executed observation are saved when `DEMO_ARTIFACT_DIR` is set.

The policy requires textual merge, absence of supported authoritative contract
incompatibilities, and recognized passing combined execution. Compiler and
runtime dependency analysis remains incomplete even when this gate passes.
Socket-restricted environments need permission to run the loopback server;
filesystem-private preview is not an OS sandbox.

## Comparative overhead method

The final CLI rerun passed the independent, broken-combination and repaired
gate assertions. Its 14 execution observations and suite arrays validated
against the integration evidence schema. The measurements below use that rerun.

The demo uses GNU `/usr/bin/time` to measure three manual private Git merge
and full-suite runs, and three Radar private merge, analysis, recommendation and
execution runs. Both paths execute the same three commands for the broken
combined fixture. Generating and copying fixture repositories is outside the
timed interval. RSS is the maximum across the measured process tree, including
Node and Python test tools. Measurements use warmed caches and the Linux
temporary filesystem, with 0.01-second timer reporting precision.

The manual workflow also catches the failure once someone selects and executes
the appropriate tests. Radar adds deterministic selection evidence, isolated
combined verification, policy gating and structured findings; no test execution
speed advantage is claimed. A negative elapsed sample from a host clock
adjustment was discarded. The demo labels such samples unavailable.

| Operation | Wall seconds, three runs | Maximum RSS, KiB |
| --- | --- | --- |
| Manual private Git merges and all three test commands | 0.78, 0.80, 0.78 | 60,148; 60,896; 60,316 |
| Radar analysis and three recommended test commands | 0.98, 1.03, 0.99 | 60,864; 60,248; 60,292 |

The read-only recommendation preview took 0.18 seconds and 15,028 KiB in a
separate single run. That sample is not a statistically established bound.

## Pinned external source evaluation

Read-only evaluation used [Pallets Click](https://github.com/pallets/click) at
`2247b35ea1c47c727d7a06e51fa280e12a863ff6`, comparing its first parent
`06b2a678741131fd577ce170e23e5ca0aeba0309`. The actual change modifies
`src/click/core.py`, three test files and documentation. No external code was
executed and no dependencies were installed.

```sh
git init /tmp/radar-click-evaluation
git -C /tmp/radar-click-evaluation fetch --depth 2 https://github.com/pallets/click.git \
  2247b35ea1c47c727d7a06e51fa280e12a863ff6
git -C /tmp/radar-click-evaluation checkout --detach 2247b35ea1c47c727d7a06e51fa280e12a863ff6
/usr/bin/time -q -f '%e %M' /tmp/radar check \
  --root /tmp/radar-click-evaluation \
  --base 06b2a678741131fd577ce170e23e5ca0aeba0309 \
  --head 2247b35ea1c47c727d7a06e51fa280e12a863ff6 --suggest-tests --json
```

The initial run inventoried 43 test files and recommended all 43. Selection
reasons were three changed-test matches and forty inferred dependency matches.
The default read-only gate passed while aggregate analysis remained incomplete;
no executed-test evidence was claimed. Executable availability established only
that Python was on PATH, not that pytest or Click dependencies were available.

This independent source exposed a concrete classifier defect:
`tests/test_arguments.py` uses pytest functions, fixtures and decorators, but
imports `mock` from `unittest`. The original lexical classifier incorrectly
recommended unittest discovery. The file's actual syntax supplies ground truth
for this single misclassification; the entire recommendation set has not been
precision-labeled. Broad impact from a central library module is plausible,
but selecting every test is not evidence of selective efficiency.

After replacing the mocking-import shortcut with actual Tree-sitter TestCase
ancestry and pytest evidence, the exact pinned comparison was repeated. All
43 files were classified pytest, including `test_arguments.py`; the recommended
command for that file became `python3 -m pytest ./tests/test_arguments.py`.
The repeated read-only run took 4.21 seconds and 65,208 KiB maximum RSS. Counts
and selection reasons otherwise stayed unchanged. Python executable availability
was true for every command; pytest dependency availability and passing tests were
not established. Raw reports were kept locally under `/tmp`, with no external
source changes.

## Radar dogfood

A committed-source read-only check compared Radar's previous commit
`d5db6d542064608c026ec099994b1bd968f9ba20` with
`342e482b148cbdca0dd90170c3167f68b5f4551b` using `check --suggest-tests`.
It inventoried 30 Go test files and proposed 16 distinct package commands.
The observation was 8.05 seconds and 31,928 KiB maximum RSS for one run on the
WSL Windows-mounted workspace. No proposed command was executed. The Click
checkout used Linux `/tmp`, so their timings are not directly comparable.

No incremental indexing or caching was introduced from these observations.
Representative monorepo and independently labeled selection evaluations remain
necessary before making scalability or recommendation-quality claims.
