# Radar 0.4 evaluation

Repository: `Pallets Click`

Pinned external revision: `2247b35ea1c47c727d7a06e51fa280e12a863ff6`

| Case | Ground truth | Classification | Gate | Selected / inventory |
| --- | --- | --- | --- | --- |
| click-intrange-clamp | ground_truth_unavailable | ground_truth_unavailable | pass | 43 / 43 |
| click-docs-only | ground_truth_unavailable | ground_truth_unavailable | pass | 0 / 43 |

## Environment and ground truth

Platform: `Linux-6.18.40.1-microsoft-standard-WSL2-x86_64-with-glibc2.39`; Python: `3.12.3`.

Radar executable SHA256: `caa84a969dab0f7c24516795d55a9c8aa9e8d10f31273c7ea35a184966421e31`.

Independent complete-suite command definitions:

```json
[
  {
    "cwd": ".",
    "argv": [
      "python3",
      "-m",
      "pytest",
      "-q",
      "tests"
    ],
    "test_files": [],
    "recognizer": "pytest"
  }
]
```

Baseline Git merge + CI command definitions:

```json
[]
```

- click-intrange-clamp: Independent existing Click IntRange tests define correct open-bound clamping; runtime remains unavailable until isolated execution.
- click-docs-only: Documentation mutation; no test failure expected, not asserted without execution.

## Aggregate observations

```json
{
  "samples": 2,
  "ground_truth_observed": 0,
  "unavailable_samples": [
    {
      "id": "click-intrange-clamp",
      "classification": "ground_truth_unavailable"
    },
    {
      "id": "click-docs-only",
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
    6.587251,
    6.376112000000001
  ],
  "total_verification_wall_seconds": [],
  "baseline_full_suite_wall_seconds": [],
  "peak_memory_kib": [
    49384,
    55716
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
