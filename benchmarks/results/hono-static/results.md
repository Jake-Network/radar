# Radar 0.4 evaluation

Repository: `Hono`

Pinned external revision: `73ff6c0e82d66468e28ed439481220f56ab03882`

| Case | Ground truth | Classification | Gate | Selected / inventory |
| --- | --- | --- | --- | --- |
| hono-controlled-source | ground_truth_unavailable | ground_truth_unavailable | pass | 118 / 118 |
| hono-docs-control | ground_truth_unavailable | ground_truth_unavailable | pass | 0 / 118 |

## Environment and ground truth

Platform: `Linux-6.18.40.1-microsoft-standard-WSL2-x86_64-with-glibc2.39`; Python: `3.12.3`.

Radar executable SHA256: `caa84a969dab0f7c24516795d55a9c8aa9e8d10f31273c7ea35a184966421e31`.

Independent complete-suite command definitions:

```json
[
  {
    "cwd": ".",
    "argv": [
      "npm",
      "test"
    ],
    "test_files": [],
    "recognizer": "vitest"
  }
]
```

Baseline Git merge + CI command definitions:

```json
[]
```

- hono-controlled-source: Upstream src/utils/url.test.ts asserts splitPath('/') and splitPath('/hello'). Controlled wrong separator is expected to violate those assertions; runtime unobserved. Exports map and multiple runtime roots complicate full-suite accounting.
- hono-docs-control: Controlled documentation-only change. No defect expected; no runtime evidence establishes absence of defects.

## Aggregate observations

```json
{
  "samples": 2,
  "ground_truth_observed": 0,
  "unavailable_samples": [
    {
      "id": "hono-controlled-source",
      "classification": "ground_truth_unavailable"
    },
    {
      "id": "hono-docs-control",
      "classification": "ground_truth_unavailable"
    }
  ],
  "integration_defect_detection_rate": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "integration_eligible_cases": [],
  "mutation_defect_detection_rate": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "known_failing_candidate_count": 0,
  "known_failing_test_recall": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "false_positive_rate": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "false_block_rate": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "uncovered_change_rate": {
    "numerator": 0,
    "denominator": 2,
    "value": 0.0
  },
  "test_selection_precision": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "test_selection_recall": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "selected_relative_to_complete_suite": {
    "numerator": 0,
    "denominator": 0,
    "value": null
  },
  "radar_analysis_overhead_seconds": [
    80.870689,
    57.669184
  ],
  "total_verification_wall_seconds": [],
  "baseline_full_suite_wall_seconds": [],
  "peak_memory_kib": [
    157928,
    160132
  ],
  "runtime_gate_peak_memory_kib": []
}
```

## Limitations

- Observed passing tests do not establish the absence of defects.
- Recall uses failing test-file groups; Go package commands group files, not individual test cases.
- Selection precision counts selected files observed failing, a lower bound on useful selection.
- GNU time reports per-command maximum RSS; it is not simultaneous aggregate process-tree memory.
- Isolation wrapper and attestation are supplied by the operator; the harness cannot certify isolation.
- Independent branch/full-suite runs and Radar execution are repeated costs; no speed claim follows from selection fraction.

Complete commands, outputs, environment, timing and provenance are in the sibling JSON report.
