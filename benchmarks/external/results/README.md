# Retained observations

Raw JSON is stored losslessly as deterministic gzip. `raw-artifacts.json` records
compressed and decompressed SHA-256 hashes; Markdown is a readable rendering,
not a replacement for raw evidence. No unsuccessful preparation attempt is
relabelled as a successful measurement.

- `baseline/chi`: initial runtime measurements; source integrity revalidated by
  `oracles/chi`, combined in `paired/chi`.
- `baseline/click-native`: initial executable with native JUnit file labels;
  `fixed/click-native`: executable with cache/runner fixes and concurrent
  attribution changes. `oracles/click` revalidates source integrity for both;
  paired artifacts preserve each original executable's measurements.
- `baseline/click` and `fixed/click`: earlier runs before native JUnit labelling;
  retain supplementary per-file baseline failure, not file-recall evidence.
- `hono-baseline`: failed preparation, read-only Vite cache/worker oversubscription;
  baseline did not qualify, so no accuracy denominator.
- `hono-oracle`: superseded oracle-only attempt intentionally stopped to run the
  complete corrected evaluation; explicitly unavailable.
- `hono-fixed`: corrected protected cache/affinity environment, native npm suite
  and Radar gate observations.
- `baseline/fullstack`: unprovisioned Python/Bun/service/browser dependencies and
  rejected repository symlink; no usable ground truth or accuracy claim.

Original artifacts retain their original harness fields and renderings, including
earlier selected-duration naming. Use paired Click/chi results and `summary.json`
for final metric semantics: attribution durations are unavailable, and passing
controls participate in precision denominators. Derived pairs are identified as
`paired_evidence`; they are not additional independently sampled defects.

Paired evidence requires matching revision, image, branch trees and candidate
tree. Ground truth comes from source-guarded revalidation; runtime latency and
conventional comparison fields come from original measurement. Different host
load and CPU affinity prevent interpreting repeated timings as controlled speedup.

`../report.py` accepts `.json` or `.json.gz` inputs. To inspect a raw artifact:

```sh
python3 -m json.tool <(gzip -dc baseline/chi.json.gz)
```
