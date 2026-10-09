# Radar 0.4 evaluation

Repository: `trusted integration fixture`

Pinned external revision: `checked-in fixture`

| Case | Ground truth | Classification | Gate | Selected / inventory |
| --- | --- | --- | --- | --- |
| combined-budget | failed | defect_detected | fail | 1 / 1 |
| harmless-price | passed | no_defect_observed | pass | 1 / 1 |

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
      "unittest",
      "test_checkout"
    ],
    "test_files": [
      "test_checkout.py"
    ],
    "recognizer": "unittest"
  }
]
```

Baseline Git merge + CI command definitions:

```json
[
  {
    "cwd": ".",
    "argv": [
      "python3",
      "-m",
      "unittest",
      "test_checkout"
    ],
    "test_files": [
      "test_checkout.py"
    ],
    "recognizer": "unittest"
  }
]
```

- combined-budget: The existing budget assertion passes on each branch and fails when price and quantity both double.
- harmless-price: Price doubles within the existing budget assertion.

## Aggregate observations

```json
{
  "samples": 2,
  "ground_truth_observed": 2,
  "unavailable_samples": [],
  "integration_defect_detection_rate": {
    "numerator": 1,
    "denominator": 1,
    "value": 1.0
  },
  "integration_eligible_cases": [
    "combined-budget"
  ],
  "mutation_defect_detection_rate": {
    "numerator": 1,
    "denominator": 1,
    "value": 1.0
  },
  "known_failing_candidate_count": 1,
  "known_failing_test_recall": {
    "numerator": 1,
    "denominator": 1,
    "value": 1.0
  },
  "false_positive_rate": {
    "numerator": 0,
    "denominator": 1,
    "value": 0.0
  },
  "false_block_rate": {
    "numerator": 0,
    "denominator": 1,
    "value": 0.0
  },
  "uncovered_change_rate": {
    "numerator": 1,
    "denominator": 3,
    "value": 0.3333333333333333
  },
  "test_selection_precision": {
    "numerator": 1,
    "denominator": 2,
    "value": 0.5
  },
  "test_selection_recall": {
    "numerator": 1,
    "denominator": 1,
    "value": 1.0
  },
  "selected_relative_to_complete_suite": {
    "numerator": 2,
    "denominator": 2,
    "value": 1.0
  },
  "radar_analysis_overhead_seconds": [
    0.408841,
    0.40870700000000004
  ],
  "total_verification_wall_seconds": [
    0.305771,
    0.285418
  ],
  "baseline_full_suite_wall_seconds": [
    0.120388,
    0.108917
  ],
  "peak_memory_kib": [
    16832,
    16704
  ],
  "runtime_gate_peak_memory_kib": [
    16988,
    16832
  ]
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
