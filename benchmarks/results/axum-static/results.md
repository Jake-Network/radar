# Radar 0.4 evaluation

Repository: `tokio-rs/axum`

Pinned external revision: `fe56a310efbb30b3b178059c48a31d1000ed5f62`

| Case | Ground truth | Classification | Gate | Selected / inventory |
| --- | --- | --- | --- | --- |
| axum-controlled-source | ground_truth_unavailable | analysis_unsupported | None | 24 / 168 |
| axum-docs-control | ground_truth_unavailable | analysis_unsupported | None | 0 / 168 |

## Environment and ground truth

Platform: `Linux-6.18.40.1-microsoft-standard-WSL2-x86_64-with-glibc2.39`; Python: `3.12.3`.

Radar executable SHA256: `caa84a969dab0f7c24516795d55a9c8aa9e8d10f31273c7ea35a184966421e31`.

Independent complete-suite command definitions:

```json
[
  {
    "cwd": ".",
    "argv": [
      "cargo",
      "test",
      "--workspace"
    ],
    "test_files": [],
    "recognizer": "cargo"
  }
]
```

Baseline Git merge + CI command definitions:

```json
[]
```

- axum-controlled-source: Upstream axum/src/routing/tests/mod.rs routes_must_start_with_slash defines route validation; inverted guard is expected to reject valid routes. Cargo workspace/internal tests are difficult inventory cases. Runtime unobserved.
- axum-docs-control: Controlled documentation-only change. No defect expected; no runtime evidence establishes absence of defects.

## Aggregate observations

```json
{
  "samples": 2,
  "ground_truth_observed": 0,
  "unavailable_samples": [
    {
      "id": "axum-controlled-source",
      "classification": "analysis_unsupported"
    },
    {
      "id": "axum-docs-control",
      "classification": "analysis_unsupported"
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
    "denominator": 0,
    "value": null
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
    9.143011999999999,
    7.518355000000001
  ],
  "total_verification_wall_seconds": [],
  "baseline_full_suite_wall_seconds": [],
  "peak_memory_kib": [
    12672,
    12544
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
