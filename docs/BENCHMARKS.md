# Benchmarks

Small, local measurements of test selection and gate behavior. They show how
Radar behaves on a few known cases. They are not evidence of recall, speed or
safety on large codebases.

## Labeled mutations

`examples/selection-eval/evaluate.py` starts from a passing commit, applies one
known defect, runs the complete suite to find which test files fail, and then
asks `radar check --suite MODE` (read-only) which files it would select.

```sh
go build -o /tmp/radar ./cmd/radar
python3 examples/selection-eval/evaluate.py fixture --radar /tmp/radar --out /tmp/eval
```

Raw results are in `examples/selection-eval/results/`.

**Fixture monorepo** (Python API, JavaScript storefront, shared JSON Schema
contract, Go module; 13 test files, 12 mutations):

| | targeted | balanced |
| --- | --- | --- |
| Mutations detected | 11 / 11 | 11 / 11 |
| Failing-file recall | 16 / 16 | 16 / 16 |
| Precision | 0.59 | 0.33 |
| Test files skipped | 83% | 69% |

This fixture was used while fixing Radar. The first run had a targeted recall
of 0.56 and exposed three bugs (Python project roots, tests that read a schema
by path, a docs-only fallback). So these numbers show the fixes work, not
unbiased recall.

**Pallets Click** at `2247b35e` (43 test files, 8 mutations chosen in advance):
7 of 7 failing files were selected. But every change under `src/click` selects
all 43 files, because each test imports `click` and its `__init__` imports
every module. Selection took about 4 s per change while the whole suite took
about 6 s. For a small, tightly coupled library, Radar saves no test time.

## Gate evaluation

`benchmarks/evaluate.py` builds branches from a manifest in a private
repository, runs the full suite independently as ground truth, and compares it
with `radar gate`. It reads local pinned checkouts and never clones, installs or
runs a shell. Executing tests of an external checkout also requires an
isolation wrapper you supply.

```sh
RADAR_BENCHMARK_BIN=/tmp/radar python3 -m unittest discover -s benchmarks -p 'test_*.py'
python3 benchmarks/evaluate.py benchmarks/manifests/monorepo.json \
  --radar /tmp/radar --allow-execution --out /tmp/radar-monorepo
```

Results are in `benchmarks/results/`. On the trusted monorepo, the gate caught
11 of 11 failing mutations and all 16 failing file groups, running 48 of 156
file observations. The documentation-only case passed the full suite but
blocked the default `--run` gate, because no test was selected. That false
block is the cost of requiring `test_selection`.

Static selection on pinned public projects (no tests were run):

| Project | Selected / discovered test files for one source change |
| --- | --- |
| FastAPI | 485 / 485 |
| Hono | 118 / 118 |
| tRPC | 23 / 183 |
| go-chi/chi | 16 / 19 |
| tokio-rs/axum | 24 / 168 (`gate` refuses the repository's symlink) |
| Pallets Click | 43 / 43 |

Documentation-only changes selected zero files in every project. Projects with
a central import hub select everything; this is a limit of file-level import
analysis.

## Indexing time

Full index plus SQLite write on Linux, warm cache: about 0.37 s for Radar's own
source (109 files), 0.2 s for a generated 1,000-file TypeScript import chain.
There is no incremental indexing yet, and no measurement on large monorepos.
