# Independent external validation

This suite extends `benchmarks/evaluate.py` with a Docker-only execution boundary.
It reuses manifest version 1, mutation edits, safe Git operations, bounded output,
recognizers and ratio helpers. `examples/selection-eval/fixture` and development
fixtures do not enter external accuracy denominators. These are controlled
challenges on independently maintained source trees, not naturally occurring bug
frequency estimates, and not broad evidence of general reliability.

## Inputs and execution

The manifests pin full upstream commit SHAs. Acquisition is a separate read-only
step; never run checkout scripts or install dependencies on the host. For example:

```sh
git clone --no-checkout https://github.com/go-chi/chi /tmp/radar-external-chi
go build -o /tmp/radar ./cmd/radar
docker build -t radar-external-python-go:local \
  -f benchmarks/external/images/python-go.Dockerfile benchmarks/external/images
docker image inspect radar-external-python-go:local --format '{{.Id}}'
python3 benchmarks/external/run.py \
  --manifest benchmarks/external/manifests/chi.json \
  --checkout /tmp/radar-external-chi --radar /tmp/radar \
  --image sha256:REPLACE_WITH_LOCAL_IMAGE_ID \
  --output /tmp/radar-external-results/chi.json --timeout 600 --repeats 2
```

Recorded local image IDs only work where those images are present. Rebuilding
produces a newly measured environment: pass its immutable ID explicitly. Base
images and Python package versions are pinned; Debian package repositories remain
mutable, so rebuilds are not asserted bit-identical. Image/tool versions and exact
input hashes accompany each run. No automatically pulled image is accepted.

Click additionally needs upstream distribution metadata, a pager and imports
from the **current candidate**, rather than an installed unmutated package:

```sh
git clone --no-checkout https://github.com/pallets/click /tmp/radar-external-click
mkdir -p /tmp/radar-click-image/source
git -C /tmp/radar-external-click archive \
  --output=/tmp/radar-click-image/source.tar 2247b35ea1c47c727d7a06e51fa280e12a863ff6
tar -xf /tmp/radar-click-image/source.tar -C /tmp/radar-click-image/source
cp benchmarks/external/images/click.Dockerfile /tmp/radar-click-image/Dockerfile
docker build -t radar-external-click:local /tmp/radar-click-image
```

The Click image builds distribution metadata in Docker, removes installed source
and uses a `.pth` entry pointing at `cwd/src`. This supplies the normal src-layout
installation requirement even when Radar sanitizes `PYTHONPATH`, without falling
back to an unmutated installed Click. The upstream default suite excludes stress
and typing jobs; those exclusions are explicit scope limits, not full release QA.
See [Hono preparation](images/hono.md) for its pinned lockfile and Node requirements.
The full-stack template is represented with a pinned revision, but its Python
3.14/Bun/PostgreSQL/browser environment is explicitly unprovisioned here.

Every evaluation container uses no network, no host credentials/environment,
non-root UID, read-only inputs/root filesystem, dropped capabilities, no new
privileges, process/memory/CPU limits and a disposable `/tmp`. Only its output
folder is writable on the host. Its public object-only checkout is read-only.
Containers are removed even after client timeout. Docker is a containment boundary,
not a guarantee against kernel vulnerabilities. Dependency preparation uses
network access in disposable image builds, separately from offline evaluation.

## Ground truth and metrics

`full_suite` is the independently executed authoritative upstream test command.
Baseline, every branch and the ordinary Git-merged combined tree execute it.
A case is integration-only only when baseline and every branch pass their full
suite, at least two branches exist, and the combined full suite fails. Expected
mutation outcomes are hypotheses until this evidence is observed.

Native pytest commands can emit fresh built-in JUnit reports (xunit1 file attributes)
via `junit_report`; bounded, safe XML binds observed cases to existing relative
files and must agree with native outcomes. This supplies Click file labels
without assuming isolated file runs preserve upstream process state.

Optional `file_oracle` commands independently label single test files. A failing
or unavailable supplementary baseline, or disagreement with the full suite,
invalidates **file recall/precision**, without replacing observed native-suite
ground truth. Optional `baseline_suite` is a separate conventional CI comparison.
Its absence defaults the timing comparison to merge plus combined native suite.
Go package tests are not independent per-file outcomes, and no file-level recall
is invented for them. Precision means selected files observed failing; it is not
a measure of all useful tests.

Targeted, balanced and full refer to Radar's supported inventory, not the entire
upstream toolchain matrix. Reports separate false PASS, false FAIL, false BLOCK,
unavailable ground truth, unavailable Radar, missing/unsupported inventory,
selection fraction, exact candidate tree, analysis time, execution time and
end-to-end time. Ratios with no eligible denominator are null. Unknown output,
zero observed tests, missing dependencies, timeout and output limits cannot pass.
Selected-file reduction is never converted into estimated runtime savings.

Cold labels apply only to a fresh baseline container/checkout; host page cache is not flushed.
No cold Radar gate is measured: gates follow oracle execution and their first
mode run already has warm tool caches.
Warm repeats share tool caches but Radar creates a fresh candidate each time.
GNU time reports process maximum RSS, **not aggregate container peak memory**.
Concurrent workload and two repetitions limit timing conclusions. Docker startup
is measured separately from worker timings. Attribution uses current gate defaults
and can add reruns; those are part of actual end-to-end latency. Selected execution
time sums the reported command durations; total test-execution time is unavailable
when attribution does not expose its rerun durations. Analysis is a separately
measured `check` probe, not instrumented internal gate phase timing.

Inputs (manifest, binary, runner and shared utility) are frozen before container
launch. JSON preserves exact output/exit status, tree identity, tool versions,
input digests, Git HEAD/diff provenance, and observations. Do not rewrite raw
results to improve scores after discovering an implementation bug. Evaluate a
new executable separately and retain both artifacts.

## CI smoke and full manual evaluation

```sh
python3 -m unittest discover -s benchmarks/external -p 'test_*.py'
RADAR_BENCHMARK_BIN=/tmp/radar python3 -m unittest discover -s benchmarks -p 'test_*.py'
```

CI runs these offline harness regressions (including malicious archives,
containment argv, immutable inputs and false integration labels). This smoke
validates the harness, not external runtime correctness. External full evaluation
is manual: prepare each image and run its manifest as above; it requires Docker,
image storage and the declared toolchains. No product checkout is modified.

## Independent revalidation and retained evidence

`--oracle-only` repeats baseline, branch and combined native suites with source
integrity guards while skipping Radar. It can qualify an earlier measurement
without rewriting its original artifact:

```sh
python3 benchmarks/external/run.py --oracle-only \
  --manifest benchmarks/external/manifests/chi.json \
  --checkout /tmp/radar-external-chi --radar /tmp/radar \
  --image sha256:REPLACE_WITH_LOCAL_IMAGE_ID \
  --output /tmp/radar-external-results/chi-oracle.json --repeats 1
python3 benchmarks/external/report.py \
  --measurements /tmp/radar-external-results/chi.json \
  --oracle /tmp/radar-external-results/chi-oracle.json \
  --output /tmp/radar-external-results/chi-paired.json
```

Pairing requires identical upstream revision, dependency image, individual branch
trees and combined candidate tree. Source guards compare HEAD and tracked changes
before/after each native suite; they cannot detect temporarily restored edits or
new untracked source files. Revalidation timings are separate from original Radar
measurements. Committed raw JSON uses lossless gzip; the pairing tool reads either
JSON or `.json.gz`. Markdown accompanies each artifact.

The worker restricts CPU affinity to two allowed cores to match its CPU quota.
Validated `runtime.writable_cache_dirs` below `/opt` receive bounded disposable
tmpfs mounts, enabling tooling caches without making dependency images writable.
Earlier attempts without these controls remain failed-preparation evidence.
