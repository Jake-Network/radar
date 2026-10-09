# Test selection validation (labeled mutations)

This measures whether Radar's static selection includes the tests that
actually fail when a known defect is introduced. It is a small, controlled
study, not evidence of production-scale recall.

## Method

For each mutation, `examples/selection-eval/evaluate.py`:

1. starts from a commit whose complete suite passes;
2. applies one defect and commits it;
3. runs the **complete** suite and records the test files that fail (ground truth);
4. runs `radar check --base BASE --head MUTANT --suite MODE --max-commands 64`
   (read-only; nothing is executed by Radar) for `targeted` and `balanced`;
5. compares selected test files with failing ones.

Recall counts failing test files that were selected. Precision counts selected
files that failed; files that pass under a mutation are not necessarily
irrelevant, so precision is a lower bound on usefulness. Reduction is
`1 − selected / inventoried` test files. Mutations whose suite cannot produce
ground truth (for example, one that hangs) are reported separately, never
silently dropped. Answers are not encoded in the engine: selection uses only
the repository's files and Radar's general rules.

Reproduce:

```sh
go build -o /tmp/radar ./cmd/radar
python3 examples/selection-eval/evaluate.py fixture --radar /tmp/radar --out /tmp/eval
# Click: prepare a checkout and an environment yourself (Radar installs nothing)
git init /tmp/click && git -C /tmp/click fetch --depth 2 https://github.com/pallets/click.git \
  2247b35ea1c47c727d7a06e51fa280e12a863ff6 && git -C /tmp/click checkout --detach FETCH_HEAD
uv venv /tmp/clickenv && uv pip install --python /tmp/clickenv/bin/python -e /tmp/click pytest
python3 examples/selection-eval/evaluate.py click --radar /tmp/radar --repo /tmp/click \
  --python /tmp/clickenv/bin/python --out /tmp/eval
```

Raw results: `examples/selection-eval/results/`.

Environment: WSL2 Linux 6.18 on a Windows host, Go 1.26.8, Python 3.12.3,
Node 24.14.0, pytest 9.1.1 (Click run only). Cargo was unavailable, so
**Rust was not evaluated**. Single runs; timings are indicative only.

## Fixture monorepo (12 mutations)

`examples/selection-eval/fixture`: a Python order API (unittest, nested
project root), a JavaScript storefront (`node:test`, ESM), a shared JSON
Schema with a declared contract binding, a Go module with three packages, a
Python cross-language integration test that loads the schema and the API
through `sys.path` at runtime, and a JS contract test that reads the schema
file. 13 test files.

| Mutation | Category | Failing test files | targeted | balanced |
| --- | --- | --- | --- | --- |
| py-discount-rounding | Python backend | 1 | 4 selected, 0 missed | 6, 0 |
| py-line-total | Python backend (transitive) | 2 | 4, 0 | 6, 0 |
| py-inventory-leak | Python backend | 1 | 1, 0 | 4, 0 |
| py-serializer-order | Python backend | 1 | 1, 0 | 4, 0 |
| api-currency-rename | shared API/schema, cross-language | 2 | 4, 0 | 7, 0 |
| schema-items-type | schema-only | 1 | 3, 0 | 3, 0 |
| js-format-padding | frontend (transitive) | 2 | 4, 0 | 5, 0 |
| js-summary-field | contract consumer | 2 | 2, 0 | 5, 0 |
| js-theme-contrast | frontend, isolated | 1 | 1, 0 | 5, 0 |
| go-clock-advance | Go, cross-package | 2 | 2, 0 | 2, 0 |
| go-health-threshold | Go, isolated | 1 | 1, 0 | 1, 0 |
| docs-only | unrelated | 0 | 0 | 0 |

| Metric | targeted | balanced |
| --- | --- | --- |
| Mutations fully detected | 11 / 11 | 11 / 11 |
| Failing-file recall | 16 / 16 (1.00) | 16 / 16 (1.00) |
| Precision | 0.59 | 0.33 |
| Test execution reduction | 83% | 69% |
| Median selection time | 0.40 s | 0.39 s |
| Full-suite fallback flagged | none | none |

**This fixture is not a held-out set.** The first run showed targeted recall of
0.56 (9 of 16 failing files) and exposed three general defects, which were
fixed before the run above:

* Python absolute imports were not resolved against an enclosing project root
  (`services/api/` with `requirements.txt`), so every Python test fell back
  to package-level selection. Recall after the fix: 0.94.
* A test that reads a changed schema by path (`integration/test_order_contract.py`)
  had no static relationship and was not reported as uncovered. The new
  `file_reference` reason selects tests that name a changed non-code file.
* A documentation-only change produced a package fallback in `balanced`.

CommonJS `require()` and literal dynamic `import()` were added after an
end-to-end test showed they were ignored. Results on this fixture therefore
demonstrate the fixed behaviors, not unbiased recall.

Timing: running all 13 files individually took 1.4 s per mutation on average
(17.0 s total). Radar selection took 4.8 s total, and the targeted files took
2.6 s.

## Pallets Click (independent, 8 mutations)

Click `2247b35e`, 43 test files, 2262 passing tests at baseline. The mutations
were chosen before measuring and were not tuned to the engine.

| Mutation | Failing test files | targeted | balanced |
| --- | --- | --- | --- |
| click-intrange-clamp (types.py) | test_types/test_IntRange.py | 43, 0 missed | 43, 0 |
| click-normalize-opt (parser.py) | test_normalization.py | 43, 0 | 43, 0 |
| click-confirm-y (termui.py) | ground truth unavailable: suite hangs in an endless prompt loop | — | — |
| click-term-len (_compat.py) | test_compat.py, test_formatting.py | 43, 0 | 43, 0 |
| click-auto-envvar (core.py) | test_context.py, test_options.py | 43, 0 | 43, 0 |
| click-split-args (shell_completion.py) | none (mutant survives the suite) | 43 | 43 |
| click-test-only | test_basic.py | 1, 0 | 1, 0 |
| click-docs-only | none | 0 | 0 |

Recall was 7 / 7 failing files (5 / 5 detectable mutations). Precision was 0.03
and reduction 28%, all of it from the test-only and docs-only changes. Every
`src/click` change selects all 43 files, because each test imports `click`,
whose `__init__` imports every module. File-level static analysis cannot be
more selective here without symbol-level usage analysis.

**Cost:** selection took about 4 s per change (28.5 s total), while the full
pytest suite took about 6 s. For a fast, centrally coupled library, Radar's
selection costs more than running the whole suite. Its value there is the
contract, gate and evidence model, not test reduction.

### Regression benchmark (prior 0.2 evaluation)

The original comparison `06b2a678..2247b35e` (changes `core.py`, three tests,
docs) still recommends all 43 files in every mode: three `changed_test` and
forty `dependency_impact` reasons. Previously these were 43 separate commands,
which `--suite recommended` refused to run (limit 16). Now they form one
required grouped command, `python3 -m pytest ./tests/... (43 paths)`, within the
default budget, with no omissions and no blocking reasons. Read-only selection
took 3.8–4.0 s and 65–73 MiB maximum RSS.

## What this does and does not show

* High recall on two small samples (19 mutations, 16 + 7 failing files). It
  does not establish recall on large or dynamically wired codebases: dynamic
  imports, dependency injection, reflection, generated code and runtime
  configuration remain unknown relationships.
* Reduction depends on coupling: 83% on a loosely coupled monorepo, none for
  core changes in Click.
* No Rust, TypeScript-with-types, Jest/Vitest or monorepo-tooling (Nx) results.
* Precision is low by design: `balanced` and package fallbacks favor recall.
